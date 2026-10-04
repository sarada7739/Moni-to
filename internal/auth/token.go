// Package auth はワンタイムトークンとセッション鍵を管理する。
//
//   - トークン：QR に載せる 1 回使い切りの値。有効期限つき。使用・失効したら即再生成する。
//   - セッション鍵：トークンと引き換えに発行する。ホストのプロセスが終了するまで有効。
//     ホーム画面から開いた場合や再接続時はこちらで認証する。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"
)

// Manager はトークンとセッションを管理する。
type Manager struct {
	mu       sync.Mutex
	ttl      time.Duration
	token    string
	expires  time.Time
	sessions map[string]struct{}
	timer    *time.Timer

	// OnTokenChange はトークンが再生成されたときに呼ばれる（ロック外で呼ぶ）。
	OnTokenChange func(token string, expires time.Time)
}

// NewManager は有効期限 ttl のトークンを扱う Manager を作る。
// 最初のトークンは Start で発行される。
func NewManager(ttl time.Duration) *Manager {
	return &Manager{ttl: ttl, sessions: map[string]struct{}{}}
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Start は最初のトークンを発行する。
func (m *Manager) Start() {
	m.regenerate()
}

// regenerate は新しいトークンを発行し、失効タイマーを張り直す。
func (m *Manager) regenerate() {
	m.mu.Lock()
	m.token = randomString(16)
	m.expires = time.Now().Add(m.ttl)
	tok, exp := m.token, m.expires
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(m.ttl, m.regenerate)
	cb := m.OnTokenChange
	m.mu.Unlock()
	if cb != nil {
		cb(tok, exp)
	}
}

// ConsumeToken はトークンを検証し、正しければ消費してセッション鍵を返す。
// 消費後はすぐに新しいトークンを発行する。
func (m *Manager) ConsumeToken(t string) (session string, ok bool) {
	m.mu.Lock()
	valid := t != "" && m.token != "" && time.Now().Before(m.expires) &&
		subtle.ConstantTimeCompare([]byte(t), []byte(m.token)) == 1
	if valid {
		m.token = "" // 二重使用を防ぐため即無効化
		session = randomString(32)
		m.sessions[session] = struct{}{}
	}
	m.mu.Unlock()
	if valid {
		m.regenerate()
	}
	return session, valid
}

// ValidSession はセッション鍵が有効かを返す。
func (m *Manager) ValidSession(s string) bool {
	if s == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[s]
	return ok
}
