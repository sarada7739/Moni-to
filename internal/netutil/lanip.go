// Package netutil は LAN 用 IP アドレスの検出を行う。
package netutil

import (
	"net"
	"sort"
	"strings"
)

// Candidate は LAN IP の候補。
type Candidate struct {
	IP        net.IP
	Interface string
	Virtual   bool // 仮想アダプタらしい名前か
	Default   bool // デフォルトルートに使われるアドレスか
}

// 仮想アダプタによく使われる名前（優先度を下げる）
var virtualHints = []string{"vethernet", "virtual", "vmware", "virtualbox", "hyper-v", "wsl", "docker", "tailscale", "zerotier", "vpn", "loopback", "bluetooth"}

func isVirtualName(name string) bool {
	l := strings.ToLower(name)
	for _, h := range virtualHints {
		if strings.Contains(l, h) {
			return true
		}
	}
	return false
}

// defaultRouteIP はデフォルトルートで使われるローカル IP を返す。
// UDP の Dial は経路決定だけでパケットは送信しない。
func defaultRouteIP() net.IP {
	c, err := net.Dial("udp4", "192.0.2.1:9") // TEST-NET-1（実際には送信されない）
	if err != nil {
		return nil
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.To4()
	}
	return nil
}

// Candidates はプライベート IPv4 アドレスの候補を優先度順に返す。
func Candidates() ([]Candidate, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	def := defaultRouteIP()
	var out []Candidate
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			out = append(out, Candidate{
				IP:        ip,
				Interface: ifc.Name,
				Virtual:   isVirtualName(ifc.Name),
				Default:   def != nil && ip.Equal(def),
			})
		}
	}
	// デフォルトルート > 物理アダプタ > 仮想アダプタ の順
	score := func(c Candidate) int {
		s := 0
		if c.Default {
			s += 10
		}
		if !c.Virtual {
			s += 5
		}
		if c.IP[0] == 192 && c.IP[1] == 168 {
			s++
		}
		return s
	}
	sort.SliceStable(out, func(i, j int) bool { return score(out[i]) > score(out[j]) })
	return out, nil
}

// InterfaceOf は ip を持つインターフェースを返す（見つからなければ nil）。
func InterfaceOf(ip net.IP) *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(ip) {
				ifc := ifc
				return &ifc
			}
		}
	}
	return nil
}
