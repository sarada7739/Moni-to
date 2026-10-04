// Package capture は FFmpeg（ddagrab）を子プロセスで起動し、画面を H.264 にエンコードする。
package capture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"moni-to/internal/display"
)

// Params はエンコード設定。
type Params struct {
	FPS         int
	BitrateKbps int           // 0 なら出力解像度と fps から自動で決める
	KeyInterval time.Duration // キーフレーム間隔
	MaxWidth    int           // 出力の最大サイズ（0 なら縮小しない）。縦長の画面では縦横を入れ替えて当てはめる
	MaxHeight   int

	// 以下は起動ごとに決まる値（FFmpeg の起動時に設定される）
	Width, Height int // 出力解像度（0 なら元のまま）
}

// AutoBitrate は解像度と fps から目安のビットレート（kbps）を返す。
// デスクトップ画面向けに 0.06 bit/画素 とし、2〜40 Mbps に収める。
func AutoBitrate(w, h, fps int) int {
	k := int(float64(w) * float64(h) * float64(fps) * 0.06 / 1000)
	if k < 2000 {
		k = 2000
	}
	if k > 40000 {
		k = 40000
	}
	return k
}

// Resolve は対象モニターの現在の大きさから、出力解像度とビットレートを決めた Params を返す。
func (p Params) Resolve(mon display.Monitor) Params {
	srcW, srcH := int(mon.Width()), int(mon.Height())
	w, h := FitSize(srcW, srcH, p.MaxWidth, p.MaxHeight)
	p.Width, p.Height = 0, 0
	if w != srcW || h != srcH {
		p.Width, p.Height = w, h
	}
	if p.BitrateKbps <= 0 {
		p.BitrateKbps = AutoBitrate(w, h, p.FPS)
	}
	return p
}

// OutputSize は出力解像度を返す（縮小しない場合はモニターの大きさ）。
func (p Params) OutputSize(mon display.Monitor) (int, int) {
	if p.Width > 0 && p.Height > 0 {
		return p.Width, p.Height
	}
	return int(mon.Width()), int(mon.Height())
}

// FitSize は src を最大サイズ（maxW×maxH）に収まるよう縦横比を保って縮小した大きさを返す。
// src が縦長なら最大サイズの縦横を入れ替えて当てはめる。拡大はしない。エンコーダの都合で偶数にそろえる。
func FitSize(srcW, srcH, maxW, maxH int) (int, int) {
	if maxW <= 0 || maxH <= 0 {
		return srcW, srcH
	}
	if (srcH > srcW) != (maxH > maxW) {
		maxW, maxH = maxH, maxW
	}
	scale := 1.0
	if r := float64(maxW) / float64(srcW); r < scale {
		scale = r
	}
	if r := float64(maxH) / float64(srcH); r < scale {
		scale = r
	}
	if scale >= 1 {
		return srcW, srcH // 縮小不要
	}
	w := int(float64(srcW)*scale) &^ 1
	h := int(float64(srcH)*scale) &^ 1
	return w, h
}

// GOP はフレーム数換算のキーフレーム間隔を返す。
func (p Params) GOP() int {
	g := int(p.KeyInterval.Seconds() * float64(p.FPS))
	if g < 1 {
		g = 1
	}
	return g
}

// Encoder は 1 種類の H.264 エンコーダの設定。
type Encoder struct {
	Name   string
	Filter string                  // ddagrab の後ろにつなぐフィルタ（先頭はカンマ）
	Args   func(p Params) []string // エンコーダ固有の引数
}

func rateArgs(p Params) []string {
	b := strconv.Itoa(p.BitrateKbps) + "k"
	// VBV バッファは 0.1 秒分。小さいほど送出のばらつき（＝遅延）が減るが、IDR の画質が落ちる
	buf := strconv.Itoa(p.BitrateKbps/10) + "k"
	return []string{"-b:v", b, "-maxrate", b, "-bufsize", buf, "-g", strconv.Itoa(p.GOP()), "-bf", "0"}
}

// Encoders は優先順のエンコーダ一覧。
// 一覧に出ても実際にハードウェアがあるとは限らないので、Probe で実際に試して選ぶ。
// どのエンコーダにも、GPU 上で NV12（BT.709）に変換済みの D3D11 フレームが渡る（buildArgs 参照）。
var Encoders = []Encoder{
	{
		// NVIDIA：D3D11 の NV12 フレームをそのまま受け取れる
		Name: "h264_nvenc",
		Args: func(p Params) []string {
			return append([]string{"-c:v", "h264_nvenc", "-preset", "p1", "-tune", "ull", "-rc", "cbr",
				"-profile:v", "baseline", "-zerolatency", "1", "-delay", "0", "-rc-lookahead", "0",
				"-forced-idr", "1", "-no-scenecut", "1"}, rateArgs(p)...)
		},
	},
	{
		// Intel：D3D11 から QSV へマップする
		Name:   "h264_qsv",
		Filter: ",hwmap=derive_device=qsv,format=qsv",
		Args: func(p Params) []string {
			return append([]string{"-c:v", "h264_qsv", "-preset", "veryfast", "-profile:v", "baseline",
				"-async_depth", "1", "-look_ahead", "0"}, rateArgs(p)...)
		},
	},
	{
		// AMD：D3D11 の NV12 フレームをそのまま受け取れる
		Name: "h264_amf",
		Args: func(p Params) []string {
			return append([]string{"-c:v", "h264_amf", "-usage", "ultralowlatency", "-quality", "speed",
				"-rc", "cbr", "-profile:v", "constrained_baseline", "-header_insertion_mode", "idr"}, rateArgs(p)...)
		},
	},
	{
		// ソフトウェア：GPU メモリから取り出す
		Name:   "libx264",
		Filter: ",hwdownload,format=nv12",
		Args: func(p Params) []string {
			return append([]string{"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
				"-profile:v", "baseline", "-sc_threshold", "0", "-keyint_min", strconv.Itoa(p.GOP())}, rateArgs(p)...)
		},
	},
	{
		// Windows 標準の Media Foundation（libx264 を含まない LGPL ビルド向けの最終手段・未検証）
		Name:   "h264_mf",
		Filter: ",hwdownload,format=nv12",
		Args: func(p Params) []string {
			return append([]string{"-c:v", "h264_mf", "-scenario", "display_remoting", "-rate_control", "cbr"}, rateArgs(p)...)
		},
	},
}

// FindEncoder は名前からエンコーダを探す。
func FindEncoder(name string) (Encoder, bool) {
	for _, e := range Encoders {
		if e.Name == name {
			return e, true
		}
	}
	return Encoder{}, false
}

// FindFFmpeg は ffmpeg.exe の場所を返す。
// 優先順：指定パス > 実行ファイルと同じフォルダ > PATH
func FindFFmpeg(override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("指定された FFmpeg が見つかりません: %s", override)
		}
		return override, nil
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "ffmpeg.exe")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	return "", errors.New("FFmpeg が見つかりません。\n" +
		"  ・ffmpeg.exe を PATH の通ったフォルダに置くか、monito.exe と同じフォルダに置いてください。\n" +
		"  ・インストール例: winget install Gyan.FFmpeg（インストール後はターミナルを開き直してください）\n" +
		"  ・または -ffmpeg フラグで ffmpeg.exe のパスを直接指定できます。")
}

// CheckFFmpeg は FFmpeg に必要な機能（ddagrab と scale_d3d11、FFmpeg 8.0 以降）があるかを確かめる。
// 古い FFmpeg だとエンコーダ確認の段階で分かりにくいエラーになるため、先に理由を示す。
func CheckFFmpeg(ffmpeg string) error {
	out, err := exec.Command(ffmpeg, "-hide_banner", "-filters").Output()
	if err != nil {
		return fmt.Errorf("FFmpeg を実行できません（%s）: %w", ffmpeg, err)
	}
	var missing []string
	for _, f := range []string{"ddagrab", "scale_d3d11"} {
		if !bytes.Contains(out, []byte(" "+f+" ")) {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	ver := "不明"
	if v, err := exec.Command(ffmpeg, "-hide_banner", "-version").Output(); err == nil {
		ver = firstLine(strings.TrimSpace(string(v)))
	}
	return fmt.Errorf("この FFmpeg には必要な機能（%s）がありません。FFmpeg 8.0 以降の Windows 向けビルドを使ってください。\n"+
		"  ・使用中: %s（%s）\n"+
		"  ・インストール例: winget install Gyan.FFmpeg", strings.Join(missing, ", "), ffmpeg, ver)
}

// buildArgs は FFmpeg のコマンドライン引数を組み立てる。
// probeFrames > 0 のときは指定フレーム数だけエンコードして破棄する（動作確認用）。
func buildArgs(enc Encoder, mon display.Monitor, p Params, probeFrames int) []string {
	// ddagrab の PTS は実キャプチャ時刻のため、エンコーダの 1/fps 単位に丸めると重複し、
	// マクサーが「非単調 DTS」としてパケットを捨ててしまう（参照が欠けて映像が乱れる）。
	// 送出側は実時間でタイムスタンプを付けるので、ここではフレーム番号で等間隔に振り直す。
	chain := fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d,setpts=N/FRAME_RATE/TB", mon.Output, p.FPS)
	// GPU 上で NV12 へ変換（必要なら縮小も）する。D3D11 のビデオプロセッサは BT.709 で変換する
	scale := "scale_d3d11=format=nv12"
	if p.Width > 0 && p.Height > 0 {
		scale += fmt.Sprintf(":width=%d:height=%d", p.Width, p.Height)
	}
	chain += "," + scale + enc.Filter + "[out]"
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		// ddagrab はこのデバイス（=モニターが接続された GPU）で Desktop Duplication を行う
		"-init_hw_device", fmt.Sprintf("d3d11va=dda:%d", mon.Adapter), "-filter_hw_device", "dda",
		"-filter_complex", chain, "-map", "[out]",
		"-fps_mode", "passthrough", "-an", "-sn",
	}
	args = append(args, enc.Args(p)...)
	// 色の規格を明示する（未指定だと受信側が BT.601 と BT.709 のどちらで解釈するか不定）
	args = append(args, "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv")
	if probeFrames > 0 {
		return append(args, "-frames:v", strconv.Itoa(probeFrames), "-f", "null", "-")
	}
	// FLV はタグごとにデータ長が先頭にあるので、フレームが届いた瞬間に取り出せる（Annex-B より 1 フレーム速い）
	return append(args, "-flush_packets", "1", "-flvflags", "no_duration_filesize", "-f", "flv", "pipe:1")
}

// Probe は実際のパイプライン（ddagrab → エンコーダ）で数フレームエンコードし、使えるか確かめる。
func Probe(ctx context.Context, ffmpeg string, enc Encoder, mon display.Monitor, p Params) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, buildArgs(enc, mon, p.Resolve(mon), 3)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if lines := strings.Split(msg, "\n"); len(lines) > 3 {
			msg = strings.Join(lines[len(lines)-3:], "\n")
		}
		return fmt.Errorf("%v: %s", err, msg)
	}
	return nil
}

// Select は候補を優先順に試し、最初に動いたエンコーダを返す。
// name が "auto" 以外ならそのエンコーダだけを試す。
func Select(ctx context.Context, ffmpeg, name string, mon display.Monitor, p Params, logf func(string, ...any)) (Encoder, error) {
	cands := Encoders
	if name != "" && name != "auto" {
		e, ok := FindEncoder(name)
		if !ok {
			return Encoder{}, fmt.Errorf("未知のエンコーダです: %s", name)
		}
		cands = []Encoder{e}
	}
	var errs []string
	for _, e := range cands {
		logf("エンコーダ確認中: %s ...", e.Name)
		if err := Probe(ctx, ffmpeg, e, mon, p); err != nil {
			logf("  → 使用不可 (%s)", firstLine(err.Error()))
			errs = append(errs, e.Name+": "+err.Error())
			continue
		}
		logf("  → 使用可能")
		return e, nil
	}
	return Encoder{}, fmt.Errorf("利用できるエンコーダがありません。\n%s", strings.Join(errs, "\n"))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
