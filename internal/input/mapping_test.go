package input

import "testing"

// 全ピクセルについて「ピクセル → 正規化値 → Windows の逆変換」で元に戻ることを確かめる
func TestToAbsoluteRoundTrip(t *testing.T) {
	cases := []struct{ origin, size int32 }{
		{0, 1920},
		{-1080, 5560}, // 縦置きモニターが左にあり、仮想デスクトップが負から始まる構成
		{-3000, 7}, {0, 1}, {0, 65536}, {0, 70000},
	}
	for _, c := range cases {
		for p := c.origin; p < c.origin+c.size; p++ {
			a := ToAbsolute(p, c.origin, c.size)
			if a < 0 || a > 65535 {
				t.Fatalf("origin=%d size=%d p=%d: 範囲外 %d", c.origin, c.size, p, a)
			}
			// 65536 を超える幅では全ピクセルを表現できないので ±1 を許容する
			got := AbsoluteToPixel(a, c.origin, c.size)
			diff := got - p
			if c.size <= 65536 && diff != 0 || diff < -1 || diff > 1 {
				t.Fatalf("origin=%d size=%d p=%d: 戻すと %d", c.origin, c.size, p, got)
			}
		}
	}
}

func TestToPixel(t *testing.T) {
	// 仮想モニターがプライマリの右（1920,0）にある想定
	mon := Rect{Left: 1920, Top: 0, Right: 1920 + 1912, Bottom: 880}
	tests := []struct {
		nx, ny       float64
		wantX, wantY int32
	}{
		{0, 0, 1920, 0},
		{1, 1, 1920 + 1911, 879},
		{0.5, 0.5, 1920 + 956, 440},
		{-0.2, 1.5, 1920, 879}, // 範囲外は端に丸める
	}
	for _, tt := range tests {
		x, y := ToPixel(mon, tt.nx, tt.ny)
		if x != tt.wantX || y != tt.wantY {
			t.Errorf("ToPixel(%v,%v) = (%d,%d), 期待 (%d,%d)", tt.nx, tt.ny, x, y, tt.wantX, tt.wantY)
		}
	}
}
