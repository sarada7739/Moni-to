// Package mdns は「monito-<PC名>.local」を LAN IP に解決させる mDNS 応答を行う。
//
// Windows 自身も <PC名>.local に応答するが、仮想アダプタ（169.254.x.x 等）の
// アドレスまで返すことがあり、iPhone が到達できない IP を選ぶ恐れがある。
// そこで別名を使い、LAN IP だけを LAN のインターフェースから返す。
package mdns

import (
	"context"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/pion/logging"
	pionmdns "github.com/pion/mdns/v2"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/windows"
)

// DefaultName は PC 名から既定のホスト名（.local を除く）を作る。
func DefaultName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "pc"
	}
	return "monito-" + Sanitize(h)
}

// Sanitize はホスト名として使える文字（英小文字・数字・ハイフン）だけにする。
func Sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// Responder は mDNS 応答器。
type Responder struct {
	conn *pionmdns.Conn
}

// Start は name（例: "monito-mypc.local"）を ip として応答し始める。
// ifc を指定するとそのインターフェースだけで応答する。
func Start(name string, ip net.IP, ifc *net.Interface) (*Responder, error) {
	// 5353 番は Windows や他のアプリ（ブラウザ等）も使うため、SO_REUSEADDR で共有する
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", pionmdns.DefaultAddressIPv4)
	if err != nil {
		return nil, err
	}

	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelDisabled
	cfg := &pionmdns.Config{
		LocalNames:    []string{name},
		LocalAddress:  ip,
		LoggerFactory: lf,
	}
	if ifc != nil {
		cfg.Interfaces = []net.Interface{*ifc}
	}
	conn, err := pionmdns.Server(ipv4.NewPacketConn(pc), nil, cfg)
	if err != nil {
		pc.Close()
		return nil, err
	}
	return &Responder{conn: conn}, nil
}

// Close は応答を止める。
func (r *Responder) Close() error {
	return r.conn.Close()
}
