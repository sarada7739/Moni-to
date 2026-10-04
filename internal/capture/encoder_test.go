package capture

import "testing"

func TestFitSize(t *testing.T) {
	tests := []struct {
		srcW, srcH, maxW, maxH int
		wantW, wantH           int
	}{
		{2868, 1320, 0, 0, 2868, 1320},       // 指定なし
		{2868, 1320, 1920, 1080, 1920, 882},  // 横長を横長の枠へ（幅で決まる、偶数化）
		{1320, 2868, 1920, 1080, 882, 1920},  // 縦長は枠を縦横入れ替えて当てはめる
		{1280, 720, 1920, 1080, 1280, 720},   // 拡大はしない
		{1920, 1080, 1280, 1280, 1280, 720},  // 正方形の枠
		{1919, 1079, 1919, 1079, 1919, 1079}, // 縮小不要なら奇数でもそのまま
	}
	for _, tt := range tests {
		w, h := FitSize(tt.srcW, tt.srcH, tt.maxW, tt.maxH)
		if w != tt.wantW || h != tt.wantH {
			t.Errorf("FitSize(%d,%d,%d,%d) = %dx%d, 期待 %dx%d", tt.srcW, tt.srcH, tt.maxW, tt.maxH, w, h, tt.wantW, tt.wantH)
		}
	}
}

func TestAutoBitrate(t *testing.T) {
	if got := AutoBitrate(1920, 1080, 60); got < 7000 || got > 8000 {
		t.Errorf("1080p60 の自動ビットレート = %d kbps（約 7.5 Mbps を想定）", got)
	}
	if got := AutoBitrate(2868, 1320, 60); got < 13000 || got > 14000 {
		t.Errorf("2868x1320@60 の自動ビットレート = %d kbps（約 13.6 Mbps を想定）", got)
	}
	if got := AutoBitrate(320, 240, 10); got != 2000 {
		t.Errorf("下限 = %d, 期待 2000", got)
	}
}
