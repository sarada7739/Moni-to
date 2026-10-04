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
	BitrateKbps int
	KeyInterval time.Duration // キーフレーム間隔
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
	// VBV バッファは 0.25 秒分。小さいほど低遅延だが IDR の画質が落ちる
	buf := strconv.Itoa(p.BitrateKbps/4) + "k"
	return []string{"-b:v", b, "-maxrate", b, "-bufsize", buf, "-g", strconv.Itoa(p.GOP()), "-bf", "0"}
}

// Encoders は優先順のエンコーダ一覧。
// 一覧に出ても実際にハードウェアがあるとは限らないので、Probe で実際に試して選ぶ。
var Encoders = []Encoder{
	{
		// NVIDIA：ddagrab の D3D11 フレームをそのまま受け取れる
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
		// AMD：D3D11 フレームをそのまま受け取れる
		Name: "h264_amf",
		Args: func(p Params) []string {
			return append([]string{"-c:v", "h264_amf", "-usage", "ultralowlatency", "-quality", "speed",
				"-rc", "cbr", "-profile:v", "constrained_baseline", "-header_insertion_mode", "idr"}, rateArgs(p)...)
		},
	},
	{
		// ソフトウェア：GPU メモリから取り出して YUV420 に変換する
		Name:   "libx264",
		Filter: ",hwdownload,format=bgra,format=yuv420p",
		Args: func(p Params) []string {
			return append([]string{"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
				"-profile:v", "baseline", "-sc_threshold", "0", "-keyint_min", strconv.Itoa(p.GOP())}, rateArgs(p)...)
		},
	},
	{
		// Windows 標準の Media Foundation（libx264 を含まない LGPL ビルド向けの最終手段・未検証）
		Name:   "h264_mf",
		Filter: ",hwdownload,format=bgra,format=nv12",
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

// buildArgs は FFmpeg のコマンドライン引数を組み立てる。
// probeFrames > 0 のときは指定フレーム数だけエンコードして破棄する（動作確認用）。
func buildArgs(enc Encoder, mon display.Monitor, p Params, probeFrames int) []string {
	// ddagrab の PTS は実キャプチャ時刻のため、エンコーダの 1/fps 単位に丸めると重複し、
	// raw h264 マクサーが「非単調 DTS」としてパケットを捨ててしまう（参照が欠けて映像が乱れる）。
	// 送出側は実時間でタイムスタンプを付けるので、ここではフレーム番号で等間隔に振り直す。
	chain := fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d,setpts=N/FRAME_RATE/TB", mon.Output, p.FPS) +
		enc.Filter + "[out]"
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		// ddagrab はこのデバイス（=モニターが接続された GPU）で Desktop Duplication を行う
		"-init_hw_device", fmt.Sprintf("d3d11va=dda:%d", mon.Adapter), "-filter_hw_device", "dda",
		"-filter_complex", chain, "-map", "[out]",
		"-fps_mode", "passthrough", "-an", "-sn",
	}
	args = append(args, enc.Args(p)...)
	// IDR ごとに SPS/PPS を付与し、各フレーム先頭に AUD を入れてフレーム境界を明確にする
	args = append(args, "-bsf:v", "dump_extra=freq=keyframe,h264_metadata=aud=insert")
	if probeFrames > 0 {
		return append(args, "-frames:v", strconv.Itoa(probeFrames), "-f", "null", "-")
	}
	return append(args, "-flush_packets", "1", "-f", "h264", "pipe:1")
}

// Probe は実際のパイプライン（ddagrab → エンコーダ）で数フレームエンコードし、使えるか確かめる。
func Probe(ctx context.Context, ffmpeg string, enc Encoder, mon display.Monitor, p Params) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, buildArgs(enc, mon, p, 3)...)
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
