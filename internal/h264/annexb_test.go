package h264

import (
	"bytes"
	"io"
	"testing"
)

// テスト用ストリームを組み立てる
func stream(nals ...[]byte) []byte {
	var b bytes.Buffer
	for i, n := range nals {
		if i%2 == 0 {
			b.Write([]byte{0, 0, 0, 1})
		} else {
			b.Write([]byte{0, 0, 1})
		}
		b.Write(n)
	}
	return b.Bytes()
}

// 1 バイトずつ返す Reader（細切れの pipe 読み込みを模擬）
type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) { return o.r.Read(p[:1]) }

var (
	aud    = []byte{0x09, 0xf0}
	sps    = []byte{0x67, 0x42, 0xe0, 0x1f, 0x11}
	pps    = []byte{0x68, 0xce, 0x38, 0x80}
	idr    = []byte{0x65, 0x88, 0x84, 0x00, 0x33}             // first_mb=0
	p1     = []byte{0x41, 0x9a, 0x02, 0x00, 0x00, 0x03, 0x01} // first_mb=0, エミュレーション防止バイト入り
	p1s2   = []byte{0x41, 0x40, 0x11}                         // 同じフレームの 2 枚目スライス（first_mb!=0）
	p2     = []byte{0x41, 0x9a, 0x05}
	nalSEI = []byte{0x06, 0x05, 0x01}
)

func TestNALReader(t *testing.T) {
	data := stream(aud, sps, pps, idr, aud, p1)
	for name, r := range map[string]io.Reader{
		"一括":    bytes.NewReader(data),
		"1バイトずつ": oneByteReader{bytes.NewReader(data)},
	} {
		nr := NewNALReader(r)
		want := [][]byte{aud, sps, pps, idr, aud, p1}
		for i, w := range want {
			got, err := nr.Next()
			if err != nil {
				t.Fatalf("%s: %d 番目でエラー: %v", name, i, err)
			}
			if !bytes.Equal(got, w) {
				t.Fatalf("%s: %d 番目 = %x, 期待 %x", name, i, got, w)
			}
		}
		if _, err := nr.Next(); err != io.EOF {
			t.Fatalf("%s: 終端で EOF にならない: %v", name, err)
		}
	}
}

func TestAssembler(t *testing.T) {
	// AUD あり：AUD|SPS|PPS|IDR , AUD|P(2スライス) , AUD|P
	// AUD なし：SEI|P , P(first_mb=0)
	seq := [][]byte{aud, sps, pps, idr, aud, p1, p1s2, aud, p2, nalSEI, p1, p2}
	var a Assembler
	var aus []*AccessUnit
	for _, n := range seq {
		if au := a.Push(n); au != nil {
			aus = append(aus, au)
		}
	}
	if au := a.Flush(); au != nil {
		aus = append(aus, au)
	}
	wantLens := []int{4, 3, 2, 2, 1}
	wantKey := []bool{true, false, false, false, false}
	if len(aus) != len(wantLens) {
		t.Fatalf("AU 数 = %d, 期待 %d", len(aus), len(wantLens))
	}
	for i, au := range aus {
		if len(au.NALs) != wantLens[i] || au.Keyframe != wantKey[i] {
			t.Errorf("AU %d: NAL 数 %d key=%v, 期待 %d key=%v", i, len(au.NALs), au.Keyframe, wantLens[i], wantKey[i])
		}
	}
	// AnnexB() は AUD を除外する
	if got := aus[0].AnnexB(); bytes.Contains(got, []byte{0, 0, 0, 1, 0x09}) {
		t.Errorf("AnnexB に AUD が残っている: %x", got)
	}
}
