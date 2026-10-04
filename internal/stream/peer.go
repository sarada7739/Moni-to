// Package stream は WebRTC の PeerConnection と視聴者を管理する。
// 同時視聴は 1 台のみ。新しい視聴者が来たら古い視聴者を切断する（後勝ち）。
package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"moni-to/internal/capture"
)

// h264FMTP は送出する H.264 の SDP パラメータ（Constrained Baseline）。
const h264FMTP = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"

// Message はシグナリングの JSON メッセージ。
type Message struct {
	Type    string `json:"type"`
	SDP     string `json:"sdp,omitempty"`
	Session string `json:"session,omitempty"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Encoder string `json:"encoder,omitempty"`
	Width   int32  `json:"width,omitempty"`
	Height  int32  `json:"height,omitempty"`
	Home    string `json:"home,omitempty"` // mDNS 名の URL（ホーム画面用）
	OK      bool   `json:"ok,omitempty"`
}

// Conn はスレッドセーフな WebSocket 送信をまとめたもの。
type Conn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

// NewConn は WebSocket をラップする。
func NewConn(ws *websocket.Conn) *Conn { return &Conn{ws: ws} }

// Send はメッセージを JSON で送る。
func (c *Conn) Send(m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.ws.WriteJSON(m)
}

// Close は WebSocket を閉じる。
func (c *Conn) Close() error { return c.ws.Close() }

// InputHandler は DataChannel で届いた操作イベントを処理する。
type InputHandler interface {
	HandleJSON(b []byte)
	ReleaseAll()
}

// Streamer は WebRTC API と現在の視聴者を保持する。
type Streamer struct {
	api        *webrtc.API
	udp        net.PacketConn
	newCapture func() *capture.Capturer
	input      InputHandler // nil なら操作を受け付けない

	// OnOrientation はクライアントの画面の向きが通知されたときに呼ばれる（nil なら無視）。
	OnOrientation func(portrait bool)

	mu  sync.Mutex
	cur *viewer
	seq int
}

// NewStreamer は lanIP:port の UDP 1 ポートで WebRTC を待ち受ける Streamer を作る。
// newCapture は視聴開始ごとに呼ばれ、新しい Capturer を返す。input は操作イベントの処理先。
func NewStreamer(lanIP net.IP, port int, newCapture func() *capture.Capturer, input InputHandler) (*Streamer, error) {
	m := &webrtc.MediaEngine{}
	fb := []webrtc.RTCPFeedback{
		{Type: "goog-remb"}, {Type: "ccm", Parameter: "fir"}, {Type: "nack"}, {Type: "nack", Parameter: "pli"},
	}
	// Safari / Chrome が提示する Constrained Baseline と Baseline の両方を受け付ける
	for i, plid := range []string{"42e01f", "42001f"} {
		err := m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=" + plid,
				RTCPFeedback: fb,
			},
			PayloadType: webrtc.PayloadType(102 + 2*i),
		}, webrtc.RTPCodecTypeVideo)
		if err != nil {
			return nil, err
		}
	}
	// NACK（再送）・RTCP レポートなど標準のインターセプタ
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		return nil, err
	}

	// WebRTC のメディアは UDP 1 ポートに集約する（ファイアウォール設定を単純にするため）
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lanIP, Port: port})
	if err != nil {
		return nil, fmt.Errorf("UDP %s:%d を開けません: %w", lanIP, port, err)
	}
	se := webrtc.SettingEngine{}
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udp))
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	// ブラウザのホスト候補は mDNS（xxxx.local）で隠されるので名前解決を有効にする
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeQueryOnly)
	se.SetICETimeouts(4*time.Second, 10*time.Second, time.Second)

	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
	return &Streamer{api: api, udp: udp, newCapture: newCapture, input: input}, nil
}

// Close は現在の視聴者を切断し、UDP ポートを閉じる。
func (s *Streamer) Close() {
	s.mu.Lock()
	v := s.cur
	s.cur = nil
	s.mu.Unlock()
	if v != nil {
		v.close("")
	}
	s.udp.Close()
}

// RestartCapture は現在の視聴者の FFmpeg を再起動する（解像度変更の後など）。
func (s *Streamer) RestartCapture() {
	s.mu.Lock()
	v := s.cur
	s.mu.Unlock()
	if v == nil {
		return
	}
	v.mu.Lock()
	c := v.capturer
	v.mu.Unlock()
	if c != nil {
		c.Restart()
	}
}

// Serve は認証済みの WebSocket で視聴者 1 台分のシグナリングを処理する（切断まで戻らない）。
// home は mDNS 名の URL で、クライアントはここへ移れるか試す。
func (s *Streamer) Serve(conn *Conn, who, home string) {
	s.mu.Lock()
	s.seq++
	v := &viewer{id: s.seq, who: who, conn: conn, s: s}
	old := s.cur
	s.cur = v
	s.mu.Unlock()

	if old != nil {
		log.Printf("[viewer] %s が接続したため %s を切断します", who, old.who)
		old.close("kicked") // 古い FFmpeg の終了まで待つ
	}
	log.Printf("[viewer] %s が接続しました", who)

	defer func() {
		v.close("")
		s.mu.Lock()
		if s.cur == v {
			s.cur = nil
		}
		s.mu.Unlock()
		log.Printf("[viewer] %s が切断しました", who)
	}()

	if err := conn.Send(Message{Type: "ready", Home: home}); err != nil {
		return
	}
	for {
		var m Message
		if err := conn.ws.ReadJSON(&m); err != nil {
			return
		}
		switch m.Type {
		case "offer":
			if err := v.handleOffer(m.SDP); err != nil {
				log.Printf("[viewer] オファー処理に失敗: %v", err)
				conn.Send(Message{Type: "error", Message: "接続の確立に失敗しました: " + err.Error()})
				return
			}
		case "homeProbe":
			if m.OK {
				log.Printf("[mdns] %s から %s に到達できました。移動します", who, home)
			} else {
				log.Printf("[mdns] %s から %s に到達できませんでした。IP の URL のまま接続します"+
					"（この状態でホーム画面に追加すると、IP が変わったときに追加し直しが必要です）", who, home)
			}
		}
	}
}

// viewer は 1 台の視聴者。
type viewer struct {
	id   int
	who  string
	conn *Conn
	s    *Streamer

	mu        sync.Mutex
	pc        *webrtc.PeerConnection
	capCancel context.CancelFunc
	capDone   chan struct{}
	capturer  *capture.Capturer
	closed    bool
}

func (v *viewer) handleOffer(sdp string) error {
	v.mu.Lock()
	if v.pc != nil || v.closed {
		v.mu.Unlock()
		return fmt.Errorf("すでにオファーを処理済みです")
	}
	pc, err := v.s.api.NewPeerConnection(webrtc.Configuration{}) // iceServers は空（LAN 内のホスト候補のみ）
	if err != nil {
		v.mu.Unlock()
		return err
	}
	v.pc = pc
	v.mu.Unlock()

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: h264FMTP},
		"screen", "monito")
	if err != nil {
		return err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		return err
	}
	go v.readRTCP(sender)

	// 操作イベント（タップ・ドラッグ・スクロール）はクライアントが作る DataChannel "input" で届く
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "input" {
			return
		}
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			v.mu.Lock()
			active := !v.closed
			v.mu.Unlock()
			if !active {
				return
			}
			var head struct {
				T        string `json:"t"`
				Portrait bool   `json:"portrait"`
			}
			if json.Unmarshal(msg.Data, &head) == nil && head.T == "orient" {
				if cb := v.s.OnOrientation; cb != nil {
					go cb(head.Portrait) // 解像度変更は時間がかかるので別ゴルーチンで
				}
				return
			}
			if v.s.input != nil {
				v.s.input.HandleJSON(msg.Data)
			}
		})
		dc.OnClose(func() {
			if v.s.input != nil {
				v.s.input.ReleaseAll()
			}
		})
	})
	pc.OnICEConnectionStateChange(func(st webrtc.ICEConnectionState) {
		log.Printf("[viewer] ICE: %s", st)
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("[viewer] 接続状態: %s", st)
		switch st {
		case webrtc.PeerConnectionStateConnected:
			v.startCapture(track)
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			go v.close("")
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		return err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	// LAN 内のホスト候補だけなので収集はすぐ終わる。Trickle せず一括で返す
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
	}
	return v.conn.Send(Message{Type: "answer", SDP: pc.LocalDescription().SDP})
}

// startCapture は FFmpeg を起動して track へ流し始める。
func (v *viewer) startCapture(track *webrtc.TrackLocalStaticSample) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.capCancel != nil {
		return
	}
	c := v.s.newCapture()
	c.OnSample = func(data []byte, dur time.Duration, key bool) {
		if err := track.WriteSample(media.Sample{Data: data, Duration: dur}); err != nil {
			log.Printf("[viewer] 送信エラー: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	v.capturer, v.capCancel, v.capDone = c, cancel, done
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	v.conn.Send(Message{Type: "info", Encoder: c.Encoder.Name, Width: c.Monitor.Width(), Height: c.Monitor.Height()})
	log.Printf("[viewer] 配信開始 (%s, %dx%d, %dfps, %dkbps)",
		c.Encoder.Name, c.Monitor.Width(), c.Monitor.Height(), c.Params.FPS, c.Params.BitrateKbps)
}

// readRTCP は受信側からの RTCP を読み、PLI/FIR ならキーフレームを要求する。
// （RTCP を読み続けないとインターセプタ（NACK 等）が動かない）
func (v *viewer) readRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, p := range pkts {
			switch p.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				v.mu.Lock()
				c := v.capturer
				v.mu.Unlock()
				if c != nil {
					c.RequestKeyframe()
				}
			}
		}
	}
}

// close は視聴者を切断する。reason が "kicked" ならクライアントに通知する。
// FFmpeg の終了まで待つ（次の視聴者のキャプチャと競合させないため）。
func (v *viewer) close(reason string) {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.closed = true
	pc, cancel, done := v.pc, v.capCancel, v.capDone
	v.mu.Unlock()

	if reason == "kicked" {
		v.conn.Send(Message{Type: "kicked", Message: "別の端末が接続したため切断しました"})
	}
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	if pc != nil {
		pc.Close()
	}
	if v.s.input != nil {
		v.s.input.ReleaseAll() // ドラッグ中に切断されてもボタンが押されたまま残らないように
	}
	v.conn.Close()
}
