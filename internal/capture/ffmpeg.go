package capture

import (
	"bytes"
	"context"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"moni-to/internal/display"
	"moni-to/internal/flv"
	"moni-to/internal/h264"
)

// SampleFunc は 1 フレーム（Annex-B）を受け取るコールバック。
type SampleFunc func(data []byte, duration time.Duration, keyframe bool)

// Capturer は FFmpeg プロセスを管理し、エンコード済みフレームを OnSample へ流す。
type Capturer struct {
	FFmpeg   string
	Encoder  Encoder
	Monitor  display.Monitor
	Params   Params
	OnSample SampleFunc

	// Resolve は FFmpeg の起動ごとに対象モニターの最新情報を返す（nil なら Monitor を使う）。
	// 自動回転で解像度が変わっても、出力サイズを追従させるため。
	Resolve func() display.Monitor

	mu          sync.Mutex
	cancelProc  context.CancelFunc // 実行中プロセスを止める
	restartReq  bool               // 再起動要求中（異常終了と区別する）
	gotFrame    bool               // 現プロセスからフレームを受け取ったか
	lastIDR     time.Time
	lastRestart time.Time
	lastSample  time.Time
	kick        chan struct{} // 再起動待ちを打ち切る
}

// Run は ctx が終わるまで FFmpeg を動かし続ける（異常終了時は再起動する）。
func (c *Capturer) Run(ctx context.Context) {
	c.mu.Lock()
	if c.kick == nil {
		c.kick = make(chan struct{}, 1)
	}
	kick := c.kick
	c.mu.Unlock()
	backoff := time.Second
	for ctx.Err() == nil {
		frames, err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		restart := c.restartReq
		c.restartReq = false
		c.mu.Unlock()
		if restart {
			continue // キーフレーム要求による意図的な再起動
		}
		if frames > 0 {
			backoff = time.Second
		}
		log.Printf("[capture] FFmpeg が終了しました (%v)。%v 後に再起動します", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		case <-kick: // Restart が呼ばれたら待たずに再起動する
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

// runOnce は FFmpeg を 1 回起動し、終了するまでフレームを読む。
func (c *Capturer) runOnce(ctx context.Context) (frames int, err error) {
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mon := c.Monitor
	if c.Resolve != nil {
		mon = c.Resolve()
	}
	params := c.Params.Resolve(mon)
	ow, oh := params.OutputSize(mon)
	log.Printf("[capture] FFmpeg 起動: %s %dx%d → 出力 %dx%d, %dfps, %dkbps",
		c.Encoder.Name, mon.Width(), mon.Height(), ow, oh, params.FPS, params.BitrateKbps)

	cmd := exec.CommandContext(pctx, c.FFmpeg, buildArgs(c.Encoder, mon, params, 0)...)
	cmd.Stderr = &lineLogger{prefix: "[ffmpeg] "}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	attachToJob(cmd.Process.Pid)

	c.mu.Lock()
	c.cancelProc = cancel
	c.gotFrame = false
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.cancelProc = nil
		c.mu.Unlock()
	}()

	fr := flv.NewReader(stdout)
	waitKey := true
	for {
		au, rerr := fr.Next()
		if rerr != nil {
			if rerr != io.EOF && pctx.Err() == nil {
				log.Printf("[capture] 出力の解析に失敗しました: %v", rerr)
			}
			break
		}
		// 再起動直後などはキーフレームが来るまで捨てる
		if waitKey && !au.Keyframe {
			continue
		}
		waitKey = false
		frames++
		c.emit(au)
	}
	io.Copy(io.Discard, stdout)
	return frames, cmd.Wait()
}

func (c *Capturer) emit(au *h264.AccessUnit) {
	now := time.Now()
	c.mu.Lock()
	dur := time.Second / time.Duration(c.Params.FPS)
	if !c.lastSample.IsZero() {
		// 実際の到着間隔を RTP タイムスタンプに反映する（再起動の空白も正しく表現される）
		dur = now.Sub(c.lastSample)
	}
	c.lastSample = now
	c.gotFrame = true
	if au.Keyframe {
		c.lastIDR = now
	}
	cb := c.OnSample
	c.mu.Unlock()
	if cb != nil {
		cb(au.AnnexB(), dur, au.Keyframe)
	}
}

// RequestKeyframe はクライアントからの PLI/FIR に応じてキーフレームを出させる。
//
// FFmpeg の CLI には実行中に外部からキーフレームを強制する手段がない。
// そのため FFmpeg を再起動して代替する（起動直後の最初のフレームは必ず IDR になる）。
// 再起動には数百 ms かかるので、次の定期 IDR がすぐ来る場合や、直前に再起動した場合は何もしない。
func (c *Capturer) RequestKeyframe() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancelProc == nil || !c.gotFrame {
		return // 起動中：最初のフレームが IDR なので不要
	}
	now := time.Now()
	if now.Sub(c.lastRestart) < time.Second {
		return // 連続した要求はまとめる
	}
	if c.lastIDR.Add(c.Params.KeyInterval).Sub(now) <= 500*time.Millisecond {
		return // 次の定期 IDR がまもなく来る
	}
	log.Printf("[capture] キーフレーム要求を受けたため FFmpeg を再起動します")
	c.lastRestart = now
	c.restartReq = true
	c.cancelProc()
}

// Restart は FFmpeg を直ちに再起動する（解像度変更の後など）。
// 解像度変更では FFmpeg が先に自分で終了していることが多いので、その場合は再起動待ちを打ち切る。
func (c *Capturer) Restart() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastRestart = time.Now()
	if c.cancelProc != nil {
		c.restartReq = true
		c.cancelProc()
		return
	}
	if c.kick != nil {
		select {
		case c.kick <- struct{}{}:
		default:
		}
	}
}

// lineLogger は FFmpeg の stderr を 1 行ずつログへ出す。
type lineLogger struct {
	prefix string
	buf    []byte
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		line := string(l.buf[:i])
		l.buf = l.buf[i+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if line != "" {
			log.Print(l.prefix + line)
		}
	}
	return len(p), nil
}

func indexNL(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}
