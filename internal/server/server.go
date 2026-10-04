// Package server はクライアントページの配信と WebSocket シグナリングの入口を提供する。
package server

import (
	"io/fs"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"moni-to/internal/auth"
	"moni-to/internal/stream"
)

func init() {
	// Go の標準 MIME 表に無いので追加する
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// Server は HTTP ハンドラ一式。
type Server struct {
	Auth     *auth.Manager
	Streamer *stream.Streamer
	Static   fs.FS

	upgrader websocket.Upgrader
}

// Handler は HTTP ハンドラを返す。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", noCache(http.FileServer(http.FS(s.Static))))
	mux.HandleFunc("/ws", s.handleWS)
	return mux
}

// noCache はページ更新が確実に反映されるようキャッシュを無効にする。
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// handleWS は WebSocket を受け付け、トークンまたはセッション鍵で認証する。
//   - ?t=<トークン>  … QR から来た初回接続。消費してセッション鍵を発行する
//   - ?s=<セッション鍵> … ホーム画面からの起動・再接続
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil) // Origin は同一ホストのみ許可（gorilla の既定）
	if err != nil {
		return
	}
	conn := stream.NewConn(ws)
	who := clientIP(r)
	q := r.URL.Query()

	switch {
	case q.Get("t") != "":
		sess, ok := s.Auth.ConsumeToken(q.Get("t"))
		if !ok {
			log.Printf("[auth] %s: 無効または期限切れのトークン", who)
			conn.Send(stream.Message{Type: "error", Code: "auth",
				Message: "QR コードが無効か期限切れです。PC に表示されている最新の QR コードを読み取ってください。"})
			closeSoon(conn)
			return
		}
		log.Printf("[auth] %s: トークン認証に成功（セッション発行）", who)
		if err := conn.Send(stream.Message{Type: "session", Session: sess}); err != nil {
			conn.Close()
			return
		}
	case s.Auth.ValidSession(q.Get("s")):
		log.Printf("[auth] %s: セッション鍵で認証", who)
	default:
		log.Printf("[auth] %s: 認証情報なし／無効なセッション", who)
		conn.Send(stream.Message{Type: "error", Code: "auth",
			Message: "接続情報が無効です（ホストが再起動された可能性があります）。PC に表示されている QR コードを読み取ってください。"})
		closeSoon(conn)
		return
	}
	s.Streamer.Serve(conn, who)
}

// closeSoon はエラーメッセージがクライアントに届くよう少し待ってから閉じる。
func closeSoon(c *stream.Conn) {
	time.AfterFunc(500*time.Millisecond, func() { c.Close() })
}

func clientIP(r *http.Request) string {
	h := r.RemoteAddr
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return h
}
