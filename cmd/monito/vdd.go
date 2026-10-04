package main

// 仮想ディスプレイ（Virtual Display Driver）の接続・切断の管理。
// monito の起動時にデスクトップへ接続し、終了時に切断する（-keep-display で切断しない）。
// 切断時の解像度と位置を保存しておき、次回の接続時に復元する。

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moni-to/internal/display"
)

// isVirtualSpec は -monitor の値が仮想ディスプレイを指すキーワードかを返す。
func isVirtualSpec(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "virtual", "vdd":
		return true
	}
	return false
}

func vddStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Moni-to", "virtual_display.json")
}

func loadPlacement() (display.Placement, bool) {
	var p display.Placement
	b, err := os.ReadFile(vddStatePath())
	if err != nil || json.Unmarshal(b, &p) != nil || p.Width <= 0 || p.Height <= 0 {
		return p, false
	}
	return p, true
}

func savePlacement(p display.Placement) {
	path := vddStatePath()
	if path == "" {
		return
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, b, 0o600)
}

// attachVirtualIfNeeded は仮想ディスプレイが切断されていれば接続する。
// 前回切断時の解像度・位置があればそれを使い、無ければ既定のモードで右端に置く。
func attachVirtualIfNeeded() {
	dev, attached, ok := display.VirtualDevice()
	if !ok || attached {
		return
	}
	p, saved := loadPlacement()
	if !saved {
		m, err := display.FirstMode(dev)
		if err != nil {
			log.Printf("仮想ディスプレイを接続できません: %v", err)
			return
		}
		p = display.Placement{Mode: m}
		if mons, err := display.List(); err == nil {
			for _, mon := range mons {
				if mon.Right > p.X {
					p.X = mon.Right
				}
			}
		}
	}
	if err := display.Attach(dev, p); err != nil {
		log.Printf("仮想ディスプレイを接続できません: %v", err)
		return
	}
	log.Printf("仮想ディスプレイ %s を接続しました（%s, 位置 %d,%d）", dev, p.Mode, p.X, p.Y)
	time.Sleep(time.Second) // DXGI に出力が現れるまで少し待つ
}

// detachVirtual は仮想ディスプレイの現在の解像度・位置を保存してから切断する。
func detachVirtual(dev string) {
	if p, err := display.CurrentPlacement(dev); err == nil {
		savePlacement(p)
	}
	if err := display.Detach(dev); err != nil {
		log.Printf("仮想ディスプレイを切断できません: %v", err)
		return
	}
	log.Printf("仮想ディスプレイ %s を切断しました（次回起動時に元の配置で接続します）", dev)
}
