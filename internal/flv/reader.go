// Package flv は FFmpeg が出力する FLV ストリームから H.264 のフレームを取り出す。
//
// Annex-B ストリームではフレームの終端が「次のフレームの先頭」が届くまで確定せず、
// 最大 1 フレーム分の遅延が生じる。FLV はタグごとにデータ長が先頭に書かれているので、
// フレームのデータが届いた時点で即座に取り出せる。
package flv

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"moni-to/internal/h264"
)

const (
	tagVideo = 9

	codecAVC = 7 // 従来形式の CodecID（H.264）

	avcSeqHeader = 0
	avcNALU      = 1

	// 拡張 FLV（Enhanced RTMP）のパケット種別
	exSequenceStart = 0
	exCodedFrames   = 1 // CompositionTime（3 バイト）付き
	exCodedFramesX  = 3 // CompositionTime なし
	exSequenceEnd   = 2
	exFourCCAVC     = "avc1"
	frameTypeKey    = 1
	maxTagSize      = 32 << 20 // 異常なデータで巨大な確保をしないための上限
)

// Reader は FLV から H.264 のアクセスユニット（1 フレーム）を読み取る。
type Reader struct {
	r           *bufio.Reader
	headerRead  bool
	pendingPrev bool // 直前のタグの PreviousTagSize（4 バイト）を未読
	lenSize     int  // NAL 長のバイト数（通常 4）
	sps, pps    [][]byte
}

// NewReader は r から読み取る Reader を作る。
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 256*1024), lenSize: 4}
}

func (fr *Reader) readHeader() error {
	var h [9]byte
	if _, err := io.ReadFull(fr.r, h[:]); err != nil {
		return err
	}
	if string(h[:3]) != "FLV" {
		return errors.New("FLV ヘッダではありません")
	}
	if off := binary.BigEndian.Uint32(h[5:9]); off > 9 {
		if _, err := fr.r.Discard(int(off - 9)); err != nil {
			return err
		}
	}
	fr.headerRead = true
	fr.pendingPrev = true // ヘッダの直後に PreviousTagSize0 がある
	return nil
}

// Next は次の映像フレームを返す。キーフレームには SPS/PPS を先頭に付ける。
func (fr *Reader) Next() (*h264.AccessUnit, error) {
	if !fr.headerRead {
		if err := fr.readHeader(); err != nil {
			return nil, err
		}
	}
	for {
		// PreviousTagSize はタグの直後に来るが、フレームを即座に返すため次の呼び出しで読み捨てる
		if fr.pendingPrev {
			if _, err := fr.r.Discard(4); err != nil {
				return nil, err
			}
			fr.pendingPrev = false
		}
		var th [11]byte
		if _, err := io.ReadFull(fr.r, th[:]); err != nil {
			return nil, err
		}
		typ := th[0] & 0x1f
		size := int(th[1])<<16 | int(th[2])<<8 | int(th[3])
		if size > maxTagSize {
			return nil, fmt.Errorf("FLV タグが大きすぎます（%d バイト）", size)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(fr.r, data); err != nil {
			return nil, err
		}
		fr.pendingPrev = true
		if typ != tagVideo {
			continue // メタデータ等は無視
		}
		au, err := fr.parseVideo(data)
		if err != nil {
			return nil, err
		}
		if au != nil {
			return au, nil
		}
	}
}

// parseVideo は映像タグのデータを解析する。フレームでなければ nil を返す。
func (fr *Reader) parseVideo(d []byte) (*h264.AccessUnit, error) {
	if len(d) < 1 {
		return nil, nil
	}
	frameType := int(d[0]>>4) & 0x07
	var packetType int
	var payload []byte

	if d[0]&0x80 != 0 {
		// 拡張 FLV：下位 4 ビットがパケット種別、続く 4 バイトが FourCC
		if len(d) < 5 {
			return nil, nil
		}
		if string(d[1:5]) != exFourCCAVC {
			return nil, fmt.Errorf("H.264 以外の映像です（%q）", d[1:5])
		}
		switch int(d[0] & 0x0f) {
		case exSequenceStart:
			packetType, payload = avcSeqHeader, d[5:]
		case exCodedFrames:
			if len(d) < 8 {
				return nil, nil
			}
			packetType, payload = avcNALU, d[8:]
		case exCodedFramesX:
			packetType, payload = avcNALU, d[5:]
		default:
			return nil, nil // シーケンス終了など
		}
	} else {
		if int(d[0]&0x0f) != codecAVC {
			return nil, fmt.Errorf("H.264 以外の映像です（CodecID %d）", d[0]&0x0f)
		}
		if len(d) < 5 {
			return nil, nil
		}
		packetType, payload = int(d[1]), d[5:] // d[2:5] は CompositionTime
	}

	switch packetType {
	case avcSeqHeader:
		return nil, fr.parseConfig(payload)
	case avcNALU:
		return fr.parseFrame(payload, frameType == frameTypeKey)
	}
	return nil, nil
}

// parseConfig は AVCDecoderConfigurationRecord から SPS/PPS と NAL 長のバイト数を取り出す。
func (fr *Reader) parseConfig(p []byte) error {
	if len(p) < 7 {
		return errors.New("AVCDecoderConfigurationRecord が短すぎます")
	}
	fr.lenSize = int(p[4]&0x03) + 1
	fr.sps, fr.pps = nil, nil
	i := 6 // p[5] は SPS の個数（下位 5 ビット）、その次から SPS が並ぶ
	readList := func(count int) ([][]byte, error) {
		var out [][]byte
		for k := 0; k < count; k++ {
			if i+2 > len(p) {
				return nil, errors.New("AVCDecoderConfigurationRecord が壊れています")
			}
			n := int(binary.BigEndian.Uint16(p[i:]))
			i += 2
			if i+n > len(p) {
				return nil, errors.New("AVCDecoderConfigurationRecord が壊れています")
			}
			out = append(out, append([]byte(nil), p[i:i+n]...))
			i += n
		}
		return out, nil
	}
	var err error
	if fr.sps, err = readList(int(p[5] & 0x1f)); err != nil {
		return err
	}
	if i >= len(p) {
		return errors.New("AVCDecoderConfigurationRecord に PPS がありません")
	}
	nPPS := int(p[i])
	i++
	fr.pps, err = readList(nPPS)
	return err
}

// parseFrame は長さ前置きの NAL 列を AccessUnit にする。
func (fr *Reader) parseFrame(p []byte, key bool) (*h264.AccessUnit, error) {
	au := &h264.AccessUnit{Keyframe: key}
	hasParams := false
	for len(p) > 0 {
		if len(p) < fr.lenSize {
			return nil, errors.New("NAL 長が途中で切れています")
		}
		n := 0
		for k := 0; k < fr.lenSize; k++ {
			n = n<<8 | int(p[k])
		}
		p = p[fr.lenSize:]
		if n > len(p) {
			return nil, errors.New("NAL が途中で切れています")
		}
		nal := p[:n]
		p = p[n:]
		switch h264.NALType(nal) {
		case h264.NALIDR:
			au.Keyframe = true
		case h264.NALSPS, h264.NALPPS:
			hasParams = true
		}
		au.NALs = append(au.NALs, nal)
	}
	if len(au.NALs) == 0 {
		return nil, nil
	}
	// キーフレームには SPS/PPS を付ける（途中から受信を始めた側もデコードできるように）
	if au.Keyframe && !hasParams {
		params := append(append([][]byte{}, fr.sps...), fr.pps...)
		au.NALs = append(params, au.NALs...)
	}
	return au, nil
}
