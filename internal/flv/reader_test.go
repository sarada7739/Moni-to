package flv

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
	"time"
)

var (
	sps = []byte{0x67, 0x42, 0xe0, 0x1f, 0x11}
	pps = []byte{0x68, 0xce, 0x38, 0x80}
	idr = []byte{0x65, 0x88, 0x84, 0x00}
	p1  = []byte{0x41, 0x9a, 0x02}
)

// テスト用 FLV を組み立てる
type builder struct{ bytes.Buffer }

func (b *builder) header() {
	b.Write([]byte{'F', 'L', 'V', 1, 1, 0, 0, 0, 9})
	b.Write([]byte{0, 0, 0, 0}) // PreviousTagSize0
}

func (b *builder) tag(typ byte, data []byte) {
	h := []byte{typ, byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data)), 0, 0, 0, 0, 0, 0, 0}
	b.Write(h)
	b.Write(data)
	var prev [4]byte
	binary.BigEndian.PutUint32(prev[:], uint32(11+len(data)))
	b.Write(prev[:])
}

func config() []byte {
	c := []byte{1, 0x42, 0xe0, 0x1f, 0xff, 0xe1, 0, byte(len(sps))}
	c = append(c, sps...)
	c = append(c, 1, 0, byte(len(pps)))
	return append(c, pps...)
}

func lenPrefixed(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		out = append(out, l[:]...)
		out = append(out, n...)
	}
	return out
}

func check(t *testing.T, r *Reader, wantKey bool, want ...[]byte) {
	t.Helper()
	au, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if au.Keyframe != wantKey || len(au.NALs) != len(want) {
		t.Fatalf("key=%v NAL数=%d, 期待 key=%v NAL数=%d", au.Keyframe, len(au.NALs), wantKey, len(want))
	}
	for i := range want {
		if !bytes.Equal(au.NALs[i], want[i]) {
			t.Fatalf("NAL %d = %x, 期待 %x", i, au.NALs[i], want[i])
		}
	}
}

func TestLegacyFLV(t *testing.T) {
	var b builder
	b.header()
	b.tag(18, []byte{2, 0, 10}) // メタデータ（無視される）
	b.tag(9, append([]byte{0x17, 0, 0, 0, 0}, config()...))
	b.tag(9, append([]byte{0x17, 1, 0, 0, 0}, lenPrefixed(idr)...))
	b.tag(9, append([]byte{0x27, 1, 0, 0, 0}, lenPrefixed(p1)...))
	r := NewReader(bytes.NewReader(b.Bytes()))
	check(t, r, true, sps, pps, idr) // キーフレームには SPS/PPS が付く
	check(t, r, false, p1)
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("終端で EOF にならない: %v", err)
	}
}

func TestEnhancedFLV(t *testing.T) {
	var b builder
	b.header()
	b.tag(9, append([]byte{0x90, 'a', 'v', 'c', '1'}, config()...))                  // SequenceStart
	b.tag(9, append([]byte{0x91, 'a', 'v', 'c', '1', 0, 0, 0}, lenPrefixed(idr)...)) // CodedFrames
	b.tag(9, append([]byte{0xa3, 'a', 'v', 'c', '1'}, lenPrefixed(p1)...))           // CodedFramesX
	r := NewReader(bytes.NewReader(b.Bytes()))
	check(t, r, true, sps, pps, idr)
	check(t, r, false, p1)
}

// フレームのデータが届いた時点で、PreviousTagSize を待たずに返せること
type stallAfter struct {
	data []byte
	pos  int
}

func (s *stallAfter) Read(p []byte) (int, error) {
	if s.pos >= len(s.data) {
		select {} // 以降のデータは永遠に来ない
	}
	n := copy(p, s.data[s.pos:])
	s.pos += n
	return n, nil
}

func TestReturnsWithoutWaitingForNextTag(t *testing.T) {
	var b builder
	b.header()
	b.tag(9, append([]byte{0x17, 0, 0, 0, 0}, config()...))
	frame := append([]byte{0x17, 1, 0, 0, 0}, lenPrefixed(idr)...)
	h := []byte{9, 0, 0, byte(len(frame)), 0, 0, 0, 0, 0, 0, 0}
	b.Write(h)
	b.Write(frame) // PreviousTagSize も次のタグも来ない
	r := NewReader(&stallAfter{data: b.Bytes()})
	type result struct {
		key bool
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		au, err := r.Next()
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{key: au.Keyframe, n: len(au.NALs)}
	}()
	select {
	case res := <-ch:
		if res.err != nil || !res.key || res.n != 3 {
			t.Fatalf("結果 %+v、期待 キーフレームで NAL 3 個", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PreviousTagSize を待ってしまい、フレームが返らない")
	}
}
