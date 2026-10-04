//go:build windows

package display

// モニターごとの拡大率（DPI スケーリング）の取得・変更。
//
// 注意：Windows はこの操作を公開 API として提供していない。
// 設定アプリが内部で使う DisplayConfigGetDeviceInfo / DisplayConfigSetDeviceInfo の
// 非公開の情報種別（-3: 取得, -4: 設定）を使う。Windows の更新で動かなくなる可能性がある。
// 値は「推奨値からの段階数」で扱われ、段階は dpiSteps の並び。

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetDisplayConfigBufferSizes = modUser32.NewProc("GetDisplayConfigBufferSizes")
	procQueryDisplayConfig          = modUser32.NewProc("QueryDisplayConfig")
	procDisplayConfigGetDeviceInfo  = modUser32.NewProc("DisplayConfigGetDeviceInfo")
	procDisplayConfigSetDeviceInfo  = modUser32.NewProc("DisplayConfigSetDeviceInfo")
)

// Windows の設定アプリが提示する拡大率の段階（%）
var dpiSteps = []int{100, 125, 150, 175, 200, 225, 250, 300, 350, 400, 450, 500}

const (
	qdcOnlyActivePaths = 0x2

	deviceInfoGetSourceName = 1
	deviceInfoGetDPIScale   = -3 // 非公開
	deviceInfoSetDPIScale   = -4 // 非公開
)

// DISPLAYCONFIG_DEVICE_INFO_HEADER
type dcHeader struct {
	Type      int32
	Size      uint32
	AdapterLo uint32
	AdapterHi int32
	ID        uint32
}

// DISPLAYCONFIG_PATH_INFO（72 バイト。必要なのは source 側の adapterId と id だけ）
type dcPathInfo struct {
	SrcAdapterLo uint32
	SrcAdapterHi int32
	SrcID        uint32
	SrcModeIdx   uint32
	SrcFlags     uint32
	_            [48]byte // targetInfo
	Flags        uint32
}

// DISPLAYCONFIG_MODE_INFO（64 バイト。中身は使わない）
type dcModeInfo struct {
	_ [64]byte
}

type dcSourceName struct {
	Header  dcHeader
	GdiName [32]uint16
}

type dcDPIGet struct {
	Header dcHeader
	MinRel int32
	CurRel int32
	MaxRel int32
}

type dcDPISet struct {
	Header dcHeader
	Rel    int32
}

// findSource は \\.\DISPLAYn に対応する表示ソース（アダプタ LUID と ID）を探す。
func findSource(device string) (dcHeader, error) {
	var nPath, nMode uint32
	if r, _, _ := procGetDisplayConfigBufferSizes.Call(qdcOnlyActivePaths,
		uintptr(unsafe.Pointer(&nPath)), uintptr(unsafe.Pointer(&nMode))); r != 0 {
		return dcHeader{}, fmt.Errorf("GetDisplayConfigBufferSizes 失敗 (%d)", r)
	}
	paths := make([]dcPathInfo, nPath)
	modes := make([]dcModeInfo, nMode)
	if nPath == 0 {
		return dcHeader{}, fmt.Errorf("有効な表示パスがありません")
	}
	var pModes uintptr
	if nMode > 0 {
		pModes = uintptr(unsafe.Pointer(&modes[0]))
	}
	if r, _, _ := procQueryDisplayConfig.Call(qdcOnlyActivePaths,
		uintptr(unsafe.Pointer(&nPath)), uintptr(unsafe.Pointer(&paths[0])),
		uintptr(unsafe.Pointer(&nMode)), pModes, 0); r != 0 {
		return dcHeader{}, fmt.Errorf("QueryDisplayConfig 失敗 (%d)", r)
	}
	for _, p := range paths[:nPath] {
		sn := new(dcSourceName)
		sn.Header = dcHeader{Type: deviceInfoGetSourceName, Size: uint32(unsafe.Sizeof(*sn)),
			AdapterLo: p.SrcAdapterLo, AdapterHi: p.SrcAdapterHi, ID: p.SrcID}
		if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(sn))); r != 0 {
			continue
		}
		if windows.UTF16ToString(sn.GdiName[:]) == device {
			return sn.Header, nil
		}
	}
	return dcHeader{}, fmt.Errorf("%s の表示ソースが見つかりません", device)
}

// scaleInfo は推奨値の段階位置と、現在・最小・最大の拡大率（%）を返す。
func scaleInfo(src dcHeader) (recIdx, cur, min, max int, err error) {
	g := new(dcDPIGet)
	g.Header = src
	g.Header.Type = deviceInfoGetDPIScale
	g.Header.Size = uint32(unsafe.Sizeof(*g))
	if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(g))); r != 0 {
		return 0, 0, 0, 0, fmt.Errorf("拡大率を取得できません (%d)", r)
	}
	// MinRel は負の値で、その絶対値が「推奨値の段階位置」になる
	recIdx = int(-g.MinRel)
	at := func(rel int32) int {
		i := recIdx + int(rel)
		if i < 0 {
			i = 0
		}
		if i >= len(dpiSteps) {
			i = len(dpiSteps) - 1
		}
		return dpiSteps[i]
	}
	return recIdx, at(g.CurRel), at(g.MinRel), at(g.MaxRel), nil
}

// Scale はモニターの現在の拡大率（%）を返す。
func Scale(device string) (int, error) {
	src, err := findSource(device)
	if err != nil {
		return 0, err
	}
	_, cur, _, _, err := scaleInfo(src)
	return cur, err
}

// SetScale はモニターの拡大率を percent（100, 125, ...）にする。
// その解像度で選べない値の場合は、選べる値まで一段ずつ下げて設定し、実際に設定した値を返す。
// （API が返す最大値は設定アプリで選べる値より小さいことがあるため、最大値では丸めず実際に試す）
func SetScale(device string, percent int) (int, error) {
	src, err := findSource(device)
	if err != nil {
		return 0, err
	}
	recIdx, cur, _, _, err := scaleInfo(src)
	if err != nil {
		return 0, err
	}
	idx := -1
	for i, v := range dpiSteps {
		if v == percent {
			idx = i
		}
	}
	if idx < 0 {
		return cur, fmt.Errorf("拡大率 %d%% は指定できません（%v のいずれか）", percent, dpiSteps)
	}
	for ; idx >= 0; idx-- {
		if dpiSteps[idx] == cur {
			return cur, nil
		}
		s := new(dcDPISet)
		s.Header = src
		s.Header.Type = deviceInfoSetDPIScale
		s.Header.Size = uint32(unsafe.Sizeof(*s))
		s.Rel = int32(idx - recIdx)
		if r, _, _ := procDisplayConfigSetDeviceInfo.Call(uintptr(unsafe.Pointer(s))); r != 0 {
			continue
		}
		// 範囲外の値は API がエラーを返さず無視することがあるので、読み戻して確かめる
		if _, now, _, _, err := scaleInfo(src); err == nil && now == dpiSteps[idx] {
			return now, nil
		}
	}
	return cur, fmt.Errorf("拡大率を %d%% に変更できません", percent)
}

// ValidScale は percent が指定可能な拡大率の段階かを返す。
func ValidScale(percent int) bool {
	for _, v := range dpiSteps {
		if v == percent {
			return true
		}
	}
	return false
}
