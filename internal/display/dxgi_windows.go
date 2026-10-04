//go:build windows

// Package display は DXGI でモニター（出力）を列挙する。
// ddagrab の output_idx は「アダプタ内の出力番号」なので、
// アダプタ番号と出力番号の組で管理する。cgo は使わず COM の vtable を直接呼ぶ。
package display

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Monitor はキャプチャ対象になりうるモニター。
type Monitor struct {
	Index       int    // このツール内での通し番号（-monitor で指定する番号）
	Adapter     int    // DXGI アダプタ番号（ffmpeg の d3d11va デバイス番号）
	Output      int    // アダプタ内の出力番号（ddagrab の output_idx）
	AdapterName string // GPU 名
	DeviceName  string // \\.\DISPLAY1 など
	Left, Top   int32  // 仮想デスクトップ上の座標（物理ピクセル）
	Right       int32
	Bottom      int32
	Primary     bool
}

// Width はモニターの幅を返す。
func (m Monitor) Width() int32 { return m.Right - m.Left }

// Height はモニターの高さを返す。
func (m Monitor) Height() int32 { return m.Bottom - m.Top }

func (m Monitor) String() string {
	p := ""
	if m.Primary {
		p = " [プライマリ]"
	}
	return fmt.Sprintf("%d: %s %dx%d @(%d,%d) / %s%s",
		m.Index, m.DeviceName, m.Width(), m.Height(), m.Left, m.Top, m.AdapterName, p)
}

var (
	modDXGI                = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory1 = modDXGI.NewProc("CreateDXGIFactory1")

	// IID_IDXGIFactory1 {770aae78-f26f-4dba-a829-253c83d1b387}
	iidDXGIFactory1 = windows.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba,
		Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
)

const (
	dxgiErrorNotFound   = 0x887A0002
	dxgiAdapterFlagSoft = 2

	// vtable のインデックス（IUnknown 3 + IDXGIObject 4 = 7 から各インターフェースのメソッド）
	vtRelease             = 2
	vtFactoryEnumAdapter1 = 12 // IDXGIFactory(5) の後の IDXGIFactory1::EnumAdapters1
	vtAdapterEnumOutputs  = 7
	vtAdapterGetDesc1     = 10 // IDXGIAdapter1::GetDesc1
	vtOutputGetDesc       = 7
)

// DXGI_OUTPUT_DESC
type outputDesc struct {
	DeviceName [32]uint16
	Left       int32
	Top        int32
	Right      int32
	Bottom     int32
	Attached   int32
	Rotation   uint32
	Monitor    uintptr
}

// DXGI_ADAPTER_DESC1
type adapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	LuidLow               uint32
	LuidHigh              int32
	Flags                 uint32
}

// comObject は COM オブジェクト（先頭が vtable へのポインタ）。
type comObject struct {
	vtbl *[32]uintptr
}

// comCall は COM オブジェクトの vtable[idx] を呼ぶ。
func comCall(obj *comObject, idx int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(obj.vtbl[idx], append([]uintptr{uintptr(unsafe.Pointer(obj))}, args...)...)
	return r
}

func release(obj *comObject) {
	if obj != nil {
		comCall(obj, vtRelease)
	}
}

// List は デスクトップに接続されたモニターをすべて返す。
func List() ([]Monitor, error) {
	if err := procCreateDXGIFactory1.Find(); err != nil {
		return nil, fmt.Errorf("dxgi.dll を読み込めません: %w", err)
	}
	// 出力先はヒープに確保する（スタック移動で uintptr が無効になるのを防ぐ）
	pFactory := new(*comObject)
	hr, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidDXGIFactory1)), uintptr(unsafe.Pointer(pFactory)))
	if hr != 0 {
		return nil, fmt.Errorf("CreateDXGIFactory1 失敗 (HRESULT 0x%08X)", uint32(hr))
	}
	factory := *pFactory
	defer release(factory)

	var mons []Monitor
	for ai := 0; ; ai++ {
		pAdapter := new(*comObject)
		hr := comCall(factory, vtFactoryEnumAdapter1, uintptr(ai), uintptr(unsafe.Pointer(pAdapter)))
		if uint32(hr) == dxgiErrorNotFound {
			break
		}
		if hr != 0 {
			return nil, fmt.Errorf("EnumAdapters1(%d) 失敗 (HRESULT 0x%08X)", ai, uint32(hr))
		}
		adapter := *pAdapter
		ad := new(adapterDesc1)
		comCall(adapter, vtAdapterGetDesc1, uintptr(unsafe.Pointer(ad)))
		adName := windows.UTF16ToString(ad.Description[:])

		if ad.Flags&dxgiAdapterFlagSoft == 0 {
			for oi := 0; ; oi++ {
				pOutput := new(*comObject)
				hr := comCall(adapter, vtAdapterEnumOutputs, uintptr(oi), uintptr(unsafe.Pointer(pOutput)))
				if hr != 0 { // DXGI_ERROR_NOT_FOUND で列挙終了
					break
				}
				output := *pOutput
				od := new(outputDesc)
				comCall(output, vtOutputGetDesc, uintptr(unsafe.Pointer(od)))
				release(output)
				if od.Attached == 0 {
					continue
				}
				mons = append(mons, Monitor{
					Index:       len(mons),
					Adapter:     ai,
					Output:      oi,
					AdapterName: adName,
					DeviceName:  windows.UTF16ToString(od.DeviceName[:]),
					Left:        od.Left, Top: od.Top, Right: od.Right, Bottom: od.Bottom,
					Primary: od.Left == 0 && od.Top == 0,
				})
			}
		}
		release(adapter)
	}
	if len(mons) == 0 {
		return nil, fmt.Errorf("キャプチャ可能なモニターが見つかりません")
	}
	return mons, nil
}

// Primary はプライマリモニターを返す。
func Primary(mons []Monitor) Monitor {
	for _, m := range mons {
		if m.Primary {
			return m
		}
	}
	return mons[0]
}

// EnableDPIAwareness はプロセスを Per-Monitor DPI Aware (V2) にする。
// これにより DXGI / SendInput の座標がスケーリングされない物理ピクセルで扱える。
func EnableDPIAwareness() {
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SetProcessDpiAwarenessContext")
	if proc.Find() == nil {
		const perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4
		proc.Call(perMonitorAwareV2)
	}
}

// Find は -monitor の指定（番号、または DISPLAY4 / \.\DISPLAY4 のような名前）からモニターを探す。
func Find(mons []Monitor, spec string) (Monitor, error) {
	s := strings.TrimSpace(spec)
	if n, err := strconv.Atoi(s); err == nil {
		for _, m := range mons {
			if m.Index == n {
				return m, nil
			}
		}
		return Monitor{}, fmt.Errorf("モニター番号 %d はありません（-list-monitors で確認してください）", n)
	}
	want := strings.ToUpper(strings.TrimPrefix(s, `\.\`))
	for _, m := range mons {
		if strings.ToUpper(strings.TrimPrefix(m.DeviceName, `\.\`)) == want {
			return m, nil
		}
	}
	return Monitor{}, fmt.Errorf("モニター %q が見つかりません（-list-monitors で確認してください）", spec)
}

// ByDeviceName は DeviceName が一致するモニターを探す（構成変更後の再解決用）。
func ByDeviceName(mons []Monitor, name string) (Monitor, bool) {
	for _, m := range mons {
		if m.DeviceName == name {
			return m, true
		}
	}
	return Monitor{}, false
}
