// Package ui はターミナルへの QR コード表示を行う。
package ui

import (
	"fmt"
	"io"
	"time"

	"github.com/mdp/qrterminal/v3"
)

// 上半分を文字色、下半分を背景色で塗る「▀」で 1 文字に 2 モジュールを描く。
// 色を明示するので、ターミナルが黒背景でも白背景でも正しい向き（白地に黒）になる。
const (
	ww = "\x1b[97;107m▀\x1b[0m" // 上:白 下:白
	bb = "\x1b[30;40m▀\x1b[0m"  // 上:黒 下:黒
	bw = "\x1b[30;107m▀\x1b[0m" // 上:黒 下:白
	wb = "\x1b[97;40m▀\x1b[0m"  // 上:白 下:黒
)

// PrintQR は URL の QR コードと URL 自体を表示する。
func PrintQR(w io.Writer, url string, expires time.Time) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "────────────────────────────────────────────")
	fmt.Fprintln(w, " iPhone の標準カメラでこの QR コードを読み取ってください")
	fmt.Fprintln(w)
	qrterminal.GenerateWithConfig(url, qrterminal.Config{
		Level:          qrterminal.L,
		Writer:         w,
		HalfBlocks:     true,
		WhiteChar:      ww,
		BlackChar:      bb,
		BlackWhiteChar: bw,
		WhiteBlackChar: wb,
		QuietZone:      2,
	})
	fmt.Fprintf(w, " URL: %s\n", url)
	fmt.Fprintf(w, " 有効期限: %s（1 回使うと新しい QR に切り替わります）\n", expires.Format("15:04:05"))
	fmt.Fprintln(w, "────────────────────────────────────────────")
}
