// Package input はクライアントからの操作イベントを Windows のマウス入力に変換する。
package input

import "math"

// Rect は物理ピクセルの矩形（Right/Bottom は含まない）。
type Rect struct {
	Left, Top, Right, Bottom int32
}

// Width は幅を返す。
func (r Rect) Width() int32 { return r.Right - r.Left }

// Height は高さを返す。
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// ToPixel は正規化座標（0〜1、映像の左上が 0）を対象モニター上のピクセル座標にする。
func ToPixel(mon Rect, nx, ny float64) (px, py int32) {
	px = mon.Left + scale(nx, mon.Width())
	py = mon.Top + scale(ny, mon.Height())
	return
}

func scale(n float64, size int32) int32 {
	if math.IsNaN(n) || n < 0 {
		n = 0
	}
	if n > 1 {
		n = 1
	}
	v := int32(math.Round(n * float64(size-1)))
	return v
}

// ToAbsolute はピクセル座標を SendInput の MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK 用の
// 0〜65535 の値に変換する。
//
// Windows は正規化値 a をピクセル floor(a * size / 65536) に対応させるので、
// ピクセルの中心 (p + 0.5) を狙って a = (2p + 1) * 65536 / (2 * size) とする。
func ToAbsolute(p, origin, size int32) int32 {
	if size <= 0 {
		return 0
	}
	off := int64(p - origin)
	if off < 0 {
		off = 0
	}
	if off >= int64(size) {
		off = int64(size) - 1
	}
	a := (2*off + 1) * 65536 / (2 * int64(size))
	if a > 65535 {
		a = 65535
	}
	return int32(a)
}

// AbsoluteToPixel は Windows が正規化値をピクセルへ戻す計算（テスト用）。
func AbsoluteToPixel(a, origin, size int32) int32 {
	return origin + int32(int64(a)*int64(size)/65536)
}
