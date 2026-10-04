// monito は Windows の画面を同じ Wi-Fi 上の iPhone（Safari）へ WebRTC でストリーミングする。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"moni-to/internal/auth"
	"moni-to/internal/capture"
	"moni-to/internal/display"
	"moni-to/internal/netutil"
	"moni-to/internal/server"
	"moni-to/internal/stream"
	"moni-to/internal/ui"
	"moni-to/web"
)

// Phase 1 の固定値（Phase 3 でフラグ化する）
const (
	defaultFPS     = 60
	defaultBitrate = 10000 // kbps
	tokenTTL       = 5 * time.Minute
)

func main() {
	var (
		port     = flag.Int("port", 8765, "HTTP（TCP）と WebRTC（UDP）で使うポート番号")
		ipFlag   = flag.String("ip", "", "LAN の IP アドレスを指定する（省略時は自動検出）")
		keyint   = flag.Duration("keyint", 2*time.Second, "キーフレーム間隔（例: 2s, 1500ms）")
		ffmpegF  = flag.String("ffmpeg", "", "ffmpeg.exe のパス（省略時は exe と同じフォルダ → PATH の順に探す）")
		encoderF = flag.String("encoder", "auto", "エンコーダ（auto, h264_nvenc, h264_qsv, h264_amf, libx264, h264_mf）")
	)
	flag.Parse()
	log.SetFlags(log.Ltime)

	enableVT()
	display.EnableDPIAwareness()

	if err := run(*port, *ipFlag, *keyint, *ffmpegF, *encoderF); err != nil {
		fmt.Fprintln(os.Stderr, "\nエラー:", err)
		os.Exit(1)
	}
}

func run(port int, ipFlag string, keyint time.Duration, ffmpegPath, encoderName string) error {
	if keyint <= 0 {
		return errors.New("-keyint には正の値を指定してください")
	}

	// LAN IP の決定
	lanIP, err := chooseIP(ipFlag)
	if err != nil {
		return err
	}

	// FFmpeg
	ff, err := capture.FindFFmpeg(ffmpegPath)
	if err != nil {
		return err
	}
	log.Printf("FFmpeg: %s", ff)

	// モニター（Phase 1 はプライマリ固定）
	mons, err := display.List()
	if err != nil {
		return err
	}
	log.Printf("モニター一覧:")
	for _, m := range mons {
		log.Printf("  %s", m)
	}
	mon := display.Primary(mons)
	log.Printf("キャプチャ対象: %s", mon)

	params := capture.Params{FPS: defaultFPS, BitrateKbps: defaultBitrate, KeyInterval: keyint}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	enc, err := capture.Select(ctx, ff, encoderName, mon, params, log.Printf)
	if err != nil {
		return err
	}
	log.Printf("使用エンコーダ: %s", enc.Name)

	// WebRTC
	streamer, err := stream.NewStreamer(lanIP, port, func() *capture.Capturer {
		return &capture.Capturer{FFmpeg: ff, Encoder: enc, Monitor: mon, Params: params}
	})
	if err != nil {
		return err
	}
	defer streamer.Close()

	// 認証と QR 表示
	am := auth.NewManager(tokenTTL)
	base := fmt.Sprintf("http://%s/", net.JoinHostPort(lanIP.String(), strconv.Itoa(port)))
	var printMu sync.Mutex
	am.OnTokenChange = func(tok string, exp time.Time) {
		printMu.Lock()
		defer printMu.Unlock()
		ui.PrintQR(os.Stdout, base+"?t="+tok, exp)
		fmt.Printf(" PC で確認する場合: http://127.0.0.1:%d/?t=%s\n\n", port, tok)
	}

	// HTTP（LAN IP と 127.0.0.1 の両方で待ち受ける。0.0.0.0 にはしない）
	srv := &server.Server{Auth: am, Streamer: streamer, Static: web.Static()}
	handler := srv.Handler()
	addrs := []string{net.JoinHostPort(lanIP.String(), strconv.Itoa(port))}
	if !lanIP.IsLoopback() {
		addrs = append(addrs, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}
	var servers []*http.Server
	errCh := make(chan error, len(addrs))
	for _, a := range addrs {
		ln, err := net.Listen("tcp4", a)
		if err != nil {
			return fmt.Errorf("%s で待ち受けできません（ポートが使用中なら -port で変更してください）: %w", a, err)
		}
		hs := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, hs)
		go func() { errCh <- hs.Serve(ln) }()
	}
	log.Printf("待ち受け開始: %v（TCP）/ %s:%d（UDP・WebRTC）", addrs, lanIP, port)
	log.Printf("初回起動時に Windows のファイアウォール確認が出たら「プライベート ネットワーク」を許可してください")

	am.Start()

	select {
	case <-ctx.Done():
		log.Printf("終了します…")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	for _, hs := range servers {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		hs.Shutdown(sctx)
		cancel()
	}
	return nil
}

// chooseIP は LAN IP を決める。-ip 指定があればそれを使い、なければ候補の先頭を使う。
func chooseIP(flagIP string) (net.IP, error) {
	cands, err := netutil.Candidates()
	if err != nil {
		return nil, fmt.Errorf("ネットワークインターフェースを取得できません: %w", err)
	}
	if len(cands) > 0 {
		log.Printf("LAN IP の候補:")
		for i, c := range cands {
			note := ""
			if c.Default {
				note += " [既定ルート]"
			}
			if c.Virtual {
				note += " [仮想アダプタ?]"
			}
			log.Printf("  %d) %-15s %s%s", i+1, c.IP, c.Interface, note)
		}
	}
	if flagIP != "" {
		ip := net.ParseIP(flagIP).To4()
		if ip == nil {
			return nil, fmt.Errorf("-ip の値が IPv4 アドレスではありません: %s", flagIP)
		}
		log.Printf("LAN IP: %s（-ip で指定）", ip)
		return ip, nil
	}
	if len(cands) == 0 {
		return nil, errors.New("LAN 用のプライベート IP アドレスが見つかりません。Wi-Fi / 有線 LAN に接続しているか確認するか、-ip で指定してください")
	}
	ip := cands[0].IP
	if len(cands) > 1 {
		log.Printf("LAN IP: %s を使用します（違う場合は -ip で指定してください）", ip)
	} else {
		log.Printf("LAN IP: %s", ip)
	}
	return ip, nil
}

// enableVT はコンソールで ANSI エスケープ（QR の色）を有効にする。
func enableVT() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) == nil {
		windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
