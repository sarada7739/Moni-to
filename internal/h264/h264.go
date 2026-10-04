// Package h264 は H.264 の NAL ユニットとアクセスユニット（=1フレーム）を扱う。
package h264

// NAL ユニットタイプ
const (
	NALSlice    = 1
	NALIDR      = 5
	NALSEI      = 6
	NALSPS      = 7
	NALPPS      = 8
	NALAUD      = 9
	NALFiller   = 12
	nalTypeMask = 0x1f
)

// NALType は NAL ユニット（スタートコードを含まない）のタイプを返す。
func NALType(nal []byte) int {
	if len(nal) == 0 {
		return 0
	}
	return int(nal[0] & nalTypeMask)
}

// AccessUnit は 1 フレーム分の NAL ユニット列。
type AccessUnit struct {
	NALs     [][]byte
	Keyframe bool // IDR スライスを含むか
}

// AnnexB は NAL をスタートコード付きで連結したバイト列を返す（pion のパケッタイザへ渡す形式）。
// AUD とフィラーは送る意味がないので除外する。
func (au *AccessUnit) AnnexB() []byte {
	size := 0
	for _, n := range au.NALs {
		size += 4 + len(n)
	}
	out := make([]byte, 0, size)
	for _, n := range au.NALs {
		t := NALType(n)
		if t == NALAUD || t == NALFiller {
			continue
		}
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}
