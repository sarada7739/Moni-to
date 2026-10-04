// Package h264 は H.264 Annex-B バイトストリームを NAL ユニットと
// アクセスユニット（=1フレーム）に分割する。
package h264

import (
	"bytes"
	"io"
)

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

// NALReader は Annex-B ストリームからスタートコード区切りで NAL ユニットを取り出す。
//
// 注意：ある NAL の終端は「次のスタートコードが来た時点」でしか確定しない。
// そのため最後の NAL（=フレーム末尾のスライス）は次フレームの先頭が届くまで
// 返せず、構造上最大 1 フレーム分の遅延が発生する。
type NALReader struct {
	r     io.Reader
	buf   []byte // 未処理データ
	chunk []byte // 読み込み用バッファ
	eof   bool
}

// NewNALReader は r から読み取る NALReader を作る。
func NewNALReader(r io.Reader) *NALReader {
	return &NALReader{r: r}
}

// findStartCode は b 内の最初のスタートコード（00 00 01）の位置と長さ（3 or 4）を返す。
func findStartCode(b []byte, from int) (pos, length int) {
	if from >= len(b) {
		return -1, 0
	}
	j := bytes.Index(b[from:], startCode3)
	if j < 0 {
		return -1, 0
	}
	p := from + j
	if p > from && b[p-1] == 0 {
		return p - 1, 4
	}
	return p, 3
}

var startCode3 = []byte{0, 0, 1}

// Next は次の NAL ユニットを返す（呼び出し側が所有するコピー）。
func (n *NALReader) Next() ([]byte, error) {
	if n.chunk == nil {
		n.chunk = make([]byte, 64*1024)
	}
	chunk := n.chunk
	for {
		// 先頭のスタートコードを探す
		start, sl := findStartCode(n.buf, 0)
		if start >= 0 {
			body := start + sl
			// 次のスタートコードを探す（見つかれば NAL 確定）
			next, _ := findStartCode(n.buf, body)
			if next >= 0 {
				nal := make([]byte, next-body)
				copy(nal, n.buf[body:next])
				n.buf = n.buf[next:]
				if len(nal) == 0 {
					continue
				}
				return nal, nil
			}
			if n.eof {
				nal := make([]byte, len(n.buf)-body)
				copy(nal, n.buf[body:])
				n.buf = nil
				if len(nal) == 0 {
					return nil, io.EOF
				}
				return nal, nil
			}
		} else if n.eof {
			return nil, io.EOF
		}

		// データを追加で読む
		k, err := n.r.Read(chunk)
		if k > 0 {
			n.buf = append(n.buf, chunk[:k]...)
		}
		if err == io.EOF {
			n.eof = true
		} else if err != nil {
			return nil, err
		}
	}
}

// AccessUnit は 1 フレーム分の NAL ユニット列。
type AccessUnit struct {
	NALs     [][]byte
	Keyframe bool // IDR スライスを含むか
}

// AnnexB は NAL をスタートコード付きで連結したバイト列を返す。
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

// Assembler は NAL 列をアクセスユニットへまとめる。
type Assembler struct {
	cur    [][]byte
	hasVCL bool
	key    bool
}

func isVCL(t int) bool { return t >= 1 && t <= 5 }

// startsNewAU は、VCL を含む AU の後にこの NAL が来たとき新しい AU の始まりかを判定する。
// (ITU-T H.264 7.4.1.2.3 を簡略化：AUD/SPS/PPS/SEI 等、または first_mb_in_slice == 0 のスライス)
func startsNewAU(nal []byte) bool {
	t := NALType(nal)
	switch {
	case t == NALAUD, t == NALSPS, t == NALPPS, t == NALSEI, t >= 14 && t <= 18:
		return true
	case isVCL(t):
		// first_mb_in_slice は ue(v)。値 0 は先頭ビット '1' で表される。
		return len(nal) > 1 && nal[1]&0x80 != 0
	}
	return false
}

// Push は NAL を 1 つ追加する。直前の AU が確定した場合はそれを返す。
func (a *Assembler) Push(nal []byte) *AccessUnit {
	var done *AccessUnit
	if a.hasVCL && startsNewAU(nal) {
		done = &AccessUnit{NALs: a.cur, Keyframe: a.key}
		a.cur, a.hasVCL, a.key = nil, false, false
	}
	t := NALType(nal)
	if isVCL(t) {
		a.hasVCL = true
		if t == NALIDR {
			a.key = true
		}
	}
	a.cur = append(a.cur, nal)
	return done
}

// Flush は保留中の AU を返す（ストリーム終端用）。
func (a *Assembler) Flush() *AccessUnit {
	if !a.hasVCL {
		return nil
	}
	done := &AccessUnit{NALs: a.cur, Keyframe: a.key}
	a.cur, a.hasVCL, a.key = nil, false, false
	return done
}
