// Package auth はワンタイムトークンと端末鍵を管理する。
//
//   - トークン：QR に載せる 1 回使い切りの値。有効期限つき。使用・失効したら即再生成する。
//   - 端末鍵：トークンと引き換えに発行する。ファイルに保存され、ホストを再起動しても有効。
//     ホーム画面から開いた場合や再接続時はこちらで認証する。-reset-devices で全て無効化できる。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Device は登録済み端末。鍵そのものは保存せず SHA-256 だけを持つ。
type Device struct {
	KeyHash  string    `json:"key_sha256"`
	Agent    string    `json:"user_agent"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
}

type deviceFile struct {
	Devices []Device `json:"devices"`
}

// Manager はトークンと端末鍵を管理する。
type Manager struct {
	mu      sync.Mutex
	ttl     time.Duration
	token   string
	expires time.Time
	timer   *time.Timer
	path    string // 端末一覧の保存先（空なら保存しない）
	devices map[string]*Device

	// OnTokenChange はトークンが再生成されたときに呼ばれる（ロック外で呼ぶ）。
	OnTokenChange func(token string, expires time.Time)
}

// DefaultDevicesPath は端末一覧の既定の保存先（%APPDATA%\Moni-to\devices.json）を返す。
func DefaultDevicesPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Moni-to", "devices.json"), nil
}

// NewManager は有効期限 ttl のトークンを扱う Manager を作り、path から端末一覧を読み込む。
// 最初のトークンは Start で発行される。
func NewManager(ttl time.Duration, path string) (*Manager, error) {
	m := &Manager{ttl: ttl, path: path, devices: map[string]*Device{}}
	if path == "" {
		return m, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var f deviceFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, errors.New(path + " が壊れています。-reset-devices で作り直してください: " + err.Error())
	}
	for i := range f.Devices {
		d := f.Devices[i]
		m.devices[d.KeyHash] = &d
	}
	return m, nil
}

// DeviceCount は登録済み端末数を返す。
func (m *Manager) DeviceCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.devices)
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashKey(k string) string {
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:])
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

// ConsumeToken はトークンを検証し、正しければ消費して端末鍵を発行・保存する。
// 消費後はすぐに新しいトークンを発行する。
func (m *Manager) ConsumeToken(t, agent string) (key string, ok bool, err error) {
	m.mu.Lock()
	valid := t != "" && m.token != "" && time.Now().Before(m.expires) &&
		subtle.ConstantTimeCompare([]byte(t), []byte(m.token)) == 1
	if valid {
		m.token = "" // 二重使用を防ぐため即無効化
		key = randomString(32)
		now := time.Now()
		m.devices[hashKey(key)] = &Device{KeyHash: hashKey(key), Agent: agent, Created: now, LastSeen: now}
		err = m.saveLocked()
	}
	m.mu.Unlock()
	if valid {
		m.regenerate()
	}
	return key, valid, err
}

// ValidKey は端末鍵が有効かを返す（有効なら最終接続日時を更新する）。
func (m *Manager) ValidKey(k string) bool {
	if k == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[hashKey(k)]
	if ok {
		d.LastSeen = time.Now()
		m.saveLocked() // 失敗しても認証には影響させない
	}
	return ok
}

// saveLocked は端末一覧をファイルへ書き出す（呼び出し側でロック済みであること）。
func (m *Manager) saveLocked() error {
	if m.path == "" {
		return nil
	}
	f := deviceFile{Devices: make([]Device, 0, len(m.devices))}
	for _, d := range m.devices {
		f.Devices = append(f.Devices, *d)
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	// 書き込み途中で落ちても壊れないよう、一時ファイルに書いてから置き換える
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
