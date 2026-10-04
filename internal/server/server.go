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
	Home     string // mDNS 名の URL（例: http://monito-mypc.local:8765）。空なら使わない

	upgrader websocket.Upgrader
}

// Handler は HTTP ハンドラを返す。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", noCache(http.FileServer(http.FS(s.Static))))
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/ping", handlePing)
	return mux
}

// handlePing は mDNS 名で到達できるかをクライアントが確かめるためのエンドポイント。
// IP の URL のページから .local の URL へ fetch するので CORS を許可する。
func handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte("ok"))
}

// noCache はページ更新が確実に反映されるようキャッシュを無効にする。
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// handleWS は WebSocket を受け付け、トークンまたは端末鍵で認証する。
//   - ?t=<トークン> … QR から来た初回接続。消費して端末鍵を発行する
//   - ?s=<端末鍵>   … ホーム画面からの起動・再接続
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
		key, ok, err := s.Auth.ConsumeToken(q.Get("t"), r.UserAgent())
		if err != nil {
			log.Printf("[auth] 端末一覧の保存に失敗しました（この端末はホスト再起動後に使えなくなります）: %v", err)
		}
		if !ok {
			log.Printf("[auth] %s: 無効または期限切れのトークン", who)
			conn.Send(stream.Message{Type: "error", Code: "auth",
				Message: "QR コードが無効か期限切れです。PC に表示されている最新の QR コードを読み取ってください。"})
			closeSoon(conn)
			return
		}
		log.Printf("[auth] %s: トークン認証に成功（端末を登録しました）", who)
		if err := conn.Send(stream.Message{Type: "session", Session: key}); err != nil {
			conn.Close()
			return
		}
	case s.Auth.ValidKey(q.Get("s")):
		log.Printf("[auth] %s: 登録済み端末として認証", who)
	default:
		log.Printf("[auth] %s: 認証情報なし／未登録の端末", who)
		conn.Send(stream.Message{Type: "error", Code: "auth",
			Message: "この端末は登録されていません（登録がリセットされた可能性があります）。PC に表示されている QR コードを読み取ってください。"})
		closeSoon(conn)
		return
	}
	s.Streamer.Serve(conn, who, s.Home)
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
