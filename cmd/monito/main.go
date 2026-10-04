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
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"moni-to/internal/auth"
	"moni-to/internal/capture"
	"moni-to/internal/display"
	"moni-to/internal/input"
	"moni-to/internal/mdns"
	"moni-to/internal/netutil"
	"moni-to/internal/server"
	"moni-to/internal/stream"
	"moni-to/internal/ui"
	"moni-to/web"
)

const tokenTTL = 5 * time.Minute

func main() {
	var (
		port     = flag.Int("port", 8765, "HTTP（TCP）と WebRTC（UDP）で使うポート番号")
		ipFlag   = flag.String("ip", "", "LAN の IP アドレスを指定する（省略時は自動検出）")
		keyint   = flag.Duration("keyint", 2*time.Second, "キーフレーム間隔（例: 2s, 1500ms）")
		fpsF     = flag.Int("fps", 60, "フレームレート")
		bitrateF = flag.Int("bitrate", 0, "ビットレート（kbps）。0 なら解像度と fps から自動")
		resF     = flag.String("resolution", "", "出力の最大解像度（例: 1920x1080）。縦横比を保って縮小し、縦長の画面では縦横を入れ替える。省略時は元のまま")
		ffmpegF  = flag.String("ffmpeg", "", "ffmpeg.exe のパス（省略時は exe と同じフォルダ → PATH の順に探す）")
		encoderF = flag.String("encoder", "auto", "エンコーダ（auto, h264_nvenc, h264_qsv, h264_amf, libx264, h264_mf）")
		nameF    = flag.String("name", "", "mDNS のホスト名（.local は不要。省略時は monito-<PC名>）")
		noMDNS   = flag.Bool("no-mdns", false, "mDNS 名（.local）を使わず IP の URL だけで動かす")
		resetDev = flag.Bool("reset-devices", false, "登録済みの端末をすべて削除してから起動する")
		monitorF = flag.String("monitor", "", "キャプチャするモニター（番号、DISPLAY4 のような名前、または virtual。省略時は仮想ディスプレイ→無ければプライマリ）")
		keepDisp = flag.Bool("keep-display", false, "終了時に仮想ディスプレイを無効化・切断しない")
		listMon  = flag.Bool("list-monitors", false, "モニター一覧を表示して終了する")
		noInput  = flag.Bool("no-input", false, "iPhone からの操作（タップ・ドラッグ・スクロール）を受け付けない")
		noRotate = flag.Bool("no-rotate", false, "iPhone の向きに合わせて仮想モニターの縦横を切り替えない")
		scaleL   = flag.Int("scale-landscape", 200, "横向き時の仮想モニターの拡大率（%）。0 で変更しない")
		scaleP   = flag.Int("scale-portrait", 125, "縦向き時の仮想モニターの拡大率（%）。0 で変更しない")
	)
	flag.Parse()
	log.SetFlags(log.Ltime)

	enableVT()
	display.EnableDPIAwareness()

	if *listMon {
		if err := listMonitors(); err != nil {
			fmt.Fprintln(os.Stderr, "エラー:", err)
			os.Exit(1)
		}
		return
	}

	opts := options{port: *port, ip: *ipFlag, keyint: *keyint, fps: *fpsF, bitrate: *bitrateF, resolution: *resF, ffmpeg: *ffmpegF, encoder: *encoderF,
		name: *nameF, noMDNS: *noMDNS, resetDevices: *resetDev, monitor: *monitorF, noInput: *noInput, noRotate: *noRotate,
		scaleLandscape: *scaleL, scalePortrait: *scaleP, keepDisplay: *keepDisp}
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "\nエラー:", err)
		os.Exit(1)
	}
}

type options struct {
	port         int
	ip           string
	keyint       time.Duration
	fps          int
	bitrate      int
	resolution   string
	ffmpeg       string
	encoder      string
	name         string
	noMDNS       bool
	resetDevices bool
	monitor      string
	noInput      bool
	noRotate     bool

	scaleLandscape int
	scalePortrait  int
	keepDisplay    bool
}

// listMonitors はモニター一覧を表示する。
func listMonitors() error {
	mons, err := display.List()
	if err != nil {
		return err
	}
	fmt.Println("モニター一覧（-monitor には番号か名前を指定します）:")
	for _, m := range mons {
		v := ""
		if display.IsVirtual(m.DeviceName) {
			v = " [仮想ディスプレイ]"
		}
		fmt.Println("  " + m.String() + v)
	}
	if dev, attached, ok := display.VirtualDevice(); ok && !attached {
		fmt.Printf("  （仮想ディスプレイ %s は切断中です。-monitor を省略するか virtual を指定すると起動時に接続します）\n", dev)
	}
	return nil
}

func run(o options) error {
	port, keyint := o.port, o.keyint
	if keyint <= 0 {
		return errors.New("-keyint には正の値を指定してください")
	}
	if o.fps < 1 || o.fps > 240 {
		return errors.New("-fps には 1〜240 を指定してください")
	}
	if o.bitrate < 0 {
		return errors.New("-bitrate には 0（自動）以上を指定してください")
	}
	for name, v := range map[string]int{"-scale-landscape": o.scaleLandscape, "-scale-portrait": o.scalePortrait} {
		if v != 0 && !display.ValidScale(v) {
			return fmt.Errorf("%s には 100, 125, 150, 175, 200, 225, 250, 300, 350, 400, 450, 500 のいずれか（変更しない場合は 0）を指定してください", name)
		}
	}

	// LAN IP の決定
	lanIP, err := chooseIP(o.ip)
	if err != nil {
		return err
	}

	// FFmpeg
	ff, err := capture.FindFFmpeg(o.ffmpeg)
	if err != nil {
		return err
	}
	log.Printf("FFmpeg: %s", ff)
	if err := capture.CheckFFmpeg(ff); err != nil {
		return err
	}

	// モニター（省略時・virtual 指定時は、切断中の仮想ディスプレイを先に接続する）
	if o.monitor == "" || isVirtualSpec(o.monitor) {
		enableVirtualIfNeeded()
		attachVirtualIfNeeded()
	}
	mons, err := display.List()
	if err != nil {
		return err
	}
	log.Printf("モニター一覧:")
	for _, m := range mons {
		log.Printf("  %s", m)
	}
	var virtual *display.Monitor
	for i := range mons {
		if display.IsVirtual(mons[i].DeviceName) {
			virtual = &mons[i]
			break
		}
	}
	var mon display.Monitor
	switch {
	case isVirtualSpec(o.monitor):
		if virtual == nil {
			return errors.New("仮想ディスプレイが見つかりません（Virtual Display Driver が入っていないか、無効になっています）")
		}
		mon = *virtual
	case o.monitor == "" && virtual != nil:
		mon = *virtual
	case o.monitor == "":
		mon = display.Primary(mons)
	default:
		if mon, err = display.Find(mons, o.monitor); err != nil {
			return err
		}
	}
	log.Printf("キャプチャ対象: %s", mon)

	// 対象モニターの最新情報を返す。モニターの抜き差しや解像度変更で番号・座標が
	// 変わっても追従できるよう、デバイス名（\\.\DISPLAYn）で引き直す
	var monMu sync.Mutex
	currentMonitor := func() display.Monitor {
		monMu.Lock()
		defer monMu.Unlock()
		if ms, err := display.List(); err == nil {
			if m, ok := display.ByDeviceName(ms, mon.DeviceName); ok {
				mon = m
			}
		}
		return mon
	}

	// 終了時に仮想ディスプレイを切断する（配信の停止後に実行されるよう、ここで defer する）
	if display.IsVirtual(mon.DeviceName) && !o.keepDisplay {
		defer func() { releaseVirtual(currentMonitor().DeviceName) }()
	}

	params := capture.Params{FPS: o.fps, BitrateKbps: o.bitrate, KeyInterval: keyint}
	if o.resolution != "" {
		if params.MaxWidth, params.MaxHeight, err = parseResolution(o.resolution); err != nil {
			return err
		}
	}

	// Ctrl+C に加え、コンソールの × ボタン（CTRL_CLOSE_EVENT → SIGTERM）でも後片付けする
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	enc, err := capture.Select(ctx, ff, o.encoder, mon, params, log.Printf)
	if err != nil {
		return err
	}
	log.Printf("使用エンコーダ: %s", enc.Name)

	// 操作（タップ・ドラッグ・スクロール）
	var inputHandler stream.InputHandler
	var injector *input.Injector
	if !o.noInput {
		injector = input.NewInjector(func() input.Rect {
			m := currentMonitor()
			return input.Rect{Left: m.Left, Top: m.Top, Right: m.Right, Bottom: m.Bottom}
		})
		inputHandler = injector
	} else {
		log.Printf("操作の受け付けは無効です（-no-input）")
	}

	// WebRTC
	streamer, err := stream.NewStreamer(lanIP, port, func() *capture.Capturer {
		return &capture.Capturer{FFmpeg: ff, Encoder: enc, Monitor: currentMonitor(), Params: params, Resolve: currentMonitor}
	}, inputHandler)
	if err != nil {
		return err
	}
	defer streamer.Close()

	// iPhone の向きに合わせて仮想モニターの縦横を切り替える（Virtual Display Driver のモニターのみ）
	switch {
	case o.noRotate:
		log.Printf("自動回転: 無効（-no-rotate）")
	case !display.IsVirtual(mon.DeviceName):
		log.Printf("自動回転: 対象が仮想モニターではないため無効（実モニターの解像度は変更しません）")
	default:
		log.Printf("自動回転: 有効（iPhone の向きに合わせて %s の縦横を切り替えます）", mon.DeviceName)
		var rotMu sync.Mutex
		streamer.OnOrientation = func(portrait bool) {
			rotMu.Lock()
			defer rotMu.Unlock()
			dev := currentMonitor().DeviceName
			dir, wantScale := "横向き", o.scaleLandscape
			if portrait {
				dir, wantScale = "縦向き", o.scalePortrait
			}
			changed, to, err := display.Orient(dev, portrait)
			if err != nil {
				log.Printf("[rotate] %v", err)
				return
			}
			if changed {
				log.Printf("[rotate] iPhone が%sになったため %s を %s に切り替えました", dir, dev, to)
				time.Sleep(300 * time.Millisecond) // 表示モードの切り替えが落ち着くのを待つ
			}
			// 拡大率は向きごとに変える（縦は幅が狭いので小さめにしないとアプリが収まらない）
			if wantScale > 0 {
				before, _ := display.Scale(dev)
				if got, err := display.SetScale(dev, wantScale); err != nil {
					log.Printf("[rotate] %v", err)
				} else if got != before {
					log.Printf("[rotate] %s の拡大率を %d%% → %d%% にしました（%s）", dev, before, got, dir)
				}
			}
			if changed {
				if injector != nil {
					injector.Invalidate()
				}
				streamer.RestartCapture()
			}
		}
	}

	// 登録済み端末
	devPath, err := auth.DefaultDevicesPath()
	if err != nil {
		return err
	}
	if o.resetDevices {
		if err := os.Remove(devPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("端末一覧を削除できません: %w", err)
		}
		log.Printf("登録済みの端末をすべて削除しました")
	}
	am, err := auth.NewManager(tokenTTL, devPath)
	if err != nil {
		return err
	}
	log.Printf("登録済み端末: %d 台（%s）", am.DeviceCount(), devPath)

	// mDNS 名（IP が変わってもホーム画面のアイコンが使えるように）
	home := ""
	if !o.noMDNS {
		name := o.name
		if name == "" {
			name = mdns.DefaultName()
		} else {
			name = mdns.Sanitize(name)
		}
		host := name + ".local"
		resp, err := mdns.Start(host, lanIP, netutil.InterfaceOf(lanIP))
		if err != nil {
			log.Printf("mDNS を開始できませんでした（IP の URL のみで動作します）: %v", err)
		} else {
			defer resp.Close()
			home = fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(port)))
			log.Printf("mDNS 名: %s → %s", host, lanIP)
		}
	}

	// 認証と QR 表示
	base := fmt.Sprintf("http://%s/", net.JoinHostPort(lanIP.String(), strconv.Itoa(port)))
	var printMu sync.Mutex
	am.OnTokenChange = func(tok string, exp time.Time) {
		printMu.Lock()
		defer printMu.Unlock()
		ui.PrintQR(os.Stdout, base+"?t="+tok, exp)
		fmt.Printf(" PC で確認する場合: http://127.0.0.1:%d/?t=%s\n\n", port, tok)
	}

	// HTTP（LAN IP と 127.0.0.1 の両方で待ち受ける。0.0.0.0 にはしない）
	srv := &server.Server{Auth: am, Streamer: streamer, Static: web.Static(), Home: home}
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

// parseResolution は "1920x1080" 形式の文字列を解析する。
func parseResolution(s string) (int, int, error) {
	var w, h int
	if _, err := fmt.Sscanf(strings.ToLower(strings.TrimSpace(s)), "%dx%d", &w, &h); err != nil || w < 16 || h < 16 {
		return 0, 0, fmt.Errorf("-resolution は 1920x1080 のように指定してください（%q）", s)
	}
	return w, h, nil
}
