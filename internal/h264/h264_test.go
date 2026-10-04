package h264

import (
	"bytes"
	"testing"
)

func TestAnnexB(t *testing.T) {
	au := &AccessUnit{NALs: [][]byte{
		{0x09, 0xf0},             // AUD（除外される）
		{0x67, 0x42, 0xe0, 0x1f}, // SPS
		{0x65, 0x88},             // IDR
	}}
	want := []byte{0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1f, 0, 0, 0, 1, 0x65, 0x88}
	if got := au.AnnexB(); !bytes.Equal(got, want) {
		t.Fatalf("AnnexB = %x, 期待 %x", got, want)
	}
}
