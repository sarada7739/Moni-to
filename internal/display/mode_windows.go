//go:build windows

package display

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32                    = windows.NewLazySystemDLL("user32.dll")
	procEnumDisplayDevicesW      = modUser32.NewProc("EnumDisplayDevicesW")
	procEnumDisplaySettingsW     = modUser32.NewProc("EnumDisplaySettingsW")
	procChangeDisplaySettingsExW = modUser32.NewProc("ChangeDisplaySettingsExW")
)

// DISPLAY_DEVICEW
type displayDevice struct {
	Cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

// DEVMODEW（ディスプレイ用のフィールド配置。220 バイト）
type devMode struct {
	DeviceName         [32]uint16
	SpecVersion        uint16
	DriverVersion      uint16
	Size               uint16
	DriverExtra        uint16
	Fields             uint32
	PositionX          int32
	PositionY          int32
	DisplayOrientation uint32
	DisplayFixedOutput uint32
	Color              int16
	Duplex             int16
	YResolution        int16
	TTOption           int16
	Collate            int16
	FormName           [32]uint16
	LogPixels          uint16
	BitsPerPel         uint32
	PelsWidth          uint32
	PelsHeight         uint32
	DisplayFlags       uint32
	DisplayFrequency   uint32
	ICMMethod          uint32
	ICMIntent          uint32
	MediaType          uint32
	DitherType         uint32
	Reserved1          uint32
	Reserved2          uint32
	PanningWidth       uint32
	PanningHeight      uint32
}

const (
	enumCurrentSettings = 0xFFFFFFFF // ENUM_CURRENT_SETTINGS

	dmPelsWidth        = 0x00080000
	dmPelsHeight       = 0x00100000
	dmDisplayFrequency = 0x00400000

	cdsUpdateRegistry = 0x00000001
)

// Mode は解像度とリフレッシュレート。
type Mode struct {
	Width, Height, Hz int
}

func (m Mode) String() string { return fmt.Sprintf("%dx%d@%d", m.Width, m.Height, m.Hz) }

// AdapterString は \\.\DISPLAYn に対応するアダプタ名（例: "Virtual Display Driver"）を返す。
func AdapterString(device string) string {
	for i := uint32(0); ; i++ {
		dd := new(displayDevice)
		dd.Cb = uint32(unsafe.Sizeof(*dd))
		r, _, _ := procEnumDisplayDevicesW.Call(0, uintptr(i), uintptr(unsafe.Pointer(dd)), 0)
		if r == 0 {
			return ""
		}
		if windows.UTF16ToString(dd.DeviceName[:]) == device {
			return windows.UTF16ToString(dd.DeviceString[:])
		}
	}
}

// IsVirtual は Virtual Display Driver のモニターかを返す。
// 実モニターの解像度を勝手に変えないよう、自動回転はこれが true のときだけ行う。
func IsVirtual(device string) bool {
	return strings.Contains(strings.ToLower(AdapterString(device)), "virtual display")
}

func enumMode(device string, i uint32) (*devMode, bool) {
	name, err := windows.UTF16PtrFromString(device)
	if err != nil {
		return nil, false
	}
	dm := new(devMode)
	dm.Size = uint16(unsafe.Sizeof(*dm))
	r, _, _ := procEnumDisplaySettingsW.Call(uintptr(unsafe.Pointer(name)), uintptr(i), uintptr(unsafe.Pointer(dm)))
	return dm, r != 0
}

// CurrentMode は現在の表示モードを返す。
func CurrentMode(device string) (Mode, error) {
	dm, ok := enumMode(device, enumCurrentSettings)
	if !ok {
		return Mode{}, fmt.Errorf("%s の現在の表示モードを取得できません", device)
	}
	return Mode{int(dm.PelsWidth), int(dm.PelsHeight), int(dm.DisplayFrequency)}, nil
}

// HasMode はそのモードが選択可能かを返す。
func HasMode(device string, m Mode) bool {
	for i := uint32(0); i < 10000; i++ {
		dm, ok := enumMode(device, i)
		if !ok {
			return false
		}
		if int(dm.PelsWidth) == m.Width && int(dm.PelsHeight) == m.Height && int(dm.DisplayFrequency) == m.Hz {
			return true
		}
	}
	return false
}

// SetMode は表示モードを変更し、設定として保存する。
func SetMode(device string, m Mode) error {
	dm, ok := enumMode(device, enumCurrentSettings)
	if !ok {
		return fmt.Errorf("%s の現在の表示モードを取得できません", device)
	}
	dm.PelsWidth, dm.PelsHeight, dm.DisplayFrequency = uint32(m.Width), uint32(m.Height), uint32(m.Hz)
	dm.Fields = dmPelsWidth | dmPelsHeight | dmDisplayFrequency
	name, _ := windows.UTF16PtrFromString(device)
	r, _, _ := procChangeDisplaySettingsExW.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(dm)), 0, cdsUpdateRegistry, 0)
	if int32(r) != 0 {
		return fmt.Errorf("表示モードを %s に変更できません（DISP_CHANGE=%d）", m, int32(r))
	}
	return nil
}

// Orient はモニターを縦向き（portrait=true）または横向きに切り替える。
// 現在の解像度の縦横を入れ替えたモードへ変更し、変更した場合は true を返す。
func Orient(device string, portrait bool) (changed bool, to Mode, err error) {
	cur, err := CurrentMode(device)
	if err != nil {
		return false, Mode{}, err
	}
	if (cur.Height > cur.Width) == portrait {
		return false, cur, nil // すでに希望の向き
	}
	to = Mode{Width: cur.Height, Height: cur.Width, Hz: cur.Hz}
	if !HasMode(device, to) {
		return false, to, fmt.Errorf(`解像度 %s が登録されていません。C:\VirtualDisplayDriver\vdd_settings.xml に %dx%d を追加してドライバを再起動してください`,
			to, to.Width, to.Height)
	}
	if err := SetMode(device, to); err != nil {
		return false, to, err
	}
	return true, to, nil
}

const (
	dmPosition        = 0x00000020
	cdsNoReset        = 0x10000000
	attachedToDesktop = 0x00000001 // DISPLAY_DEVICE_ATTACHED_TO_DESKTOP
)

// VirtualDevice は Virtual Display Driver の表示デバイス名と、デスクトップに接続中かを返す。
func VirtualDevice() (name string, attached bool, ok bool) {
	for i := uint32(0); ; i++ {
		dd := new(displayDevice)
		dd.Cb = uint32(unsafe.Sizeof(*dd))
		r, _, _ := procEnumDisplayDevicesW.Call(0, uintptr(i), uintptr(unsafe.Pointer(dd)), 0)
		if r == 0 {
			return "", false, false
		}
		if strings.Contains(strings.ToLower(windows.UTF16ToString(dd.DeviceString[:])), "virtual display") {
			return windows.UTF16ToString(dd.DeviceName[:]), dd.StateFlags&attachedToDesktop != 0, true
		}
	}
}

// Placement はモニターの表示モードと仮想デスクトップ上の位置。
type Placement struct {
	Mode
	X, Y int32
}

// CurrentPlacement は現在の表示モードと位置を返す。
func CurrentPlacement(device string) (Placement, error) {
	dm, ok := enumMode(device, enumCurrentSettings)
	if !ok {
		return Placement{}, fmt.Errorf("%s の現在の表示モードを取得できません", device)
	}
	return Placement{Mode{int(dm.PelsWidth), int(dm.PelsHeight), int(dm.DisplayFrequency)}, dm.PositionX, dm.PositionY}, nil
}

func applyDisplayChanges() error {
	r, _, _ := procChangeDisplaySettingsExW.Call(0, 0, 0, 0, 0)
	if int32(r) != 0 {
		return fmt.Errorf("表示設定を適用できません（DISP_CHANGE=%d）", int32(r))
	}
	return nil
}

// Detach はモニターをデスクトップから外す（設定アプリの「このディスプレイを切断する」と同じ）。
func Detach(device string) error {
	dm := new(devMode)
	dm.Size = uint16(unsafe.Sizeof(*dm))
	dm.Fields = dmPosition | dmPelsWidth | dmPelsHeight // 幅・高さ 0 で切断
	name, _ := windows.UTF16PtrFromString(device)
	r, _, _ := procChangeDisplaySettingsExW.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(dm)), 0,
		cdsUpdateRegistry|cdsNoReset, 0)
	if int32(r) != 0 {
		return fmt.Errorf("%s を切断できません（DISP_CHANGE=%d）", device, int32(r))
	}
	return applyDisplayChanges()
}

// Attach はモニターを指定の表示モード・位置でデスクトップに接続する。
func Attach(device string, p Placement) error {
	dm := new(devMode)
	dm.Size = uint16(unsafe.Sizeof(*dm))
	dm.Fields = dmPosition | dmPelsWidth | dmPelsHeight | dmDisplayFrequency
	dm.PositionX, dm.PositionY = p.X, p.Y
	dm.PelsWidth, dm.PelsHeight, dm.DisplayFrequency = uint32(p.Width), uint32(p.Height), uint32(p.Hz)
	name, _ := windows.UTF16PtrFromString(device)
	r, _, _ := procChangeDisplaySettingsExW.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(dm)), 0,
		cdsUpdateRegistry|cdsNoReset, 0)
	if int32(r) != 0 {
		return fmt.Errorf("%s を接続できません（DISP_CHANGE=%d）", device, int32(r))
	}
	return applyDisplayChanges()
}

// FirstMode は選択可能な最初の表示モード（ドライバが先頭に挙げるもの）を返す。
func FirstMode(device string) (Mode, error) {
	dm, ok := enumMode(device, 0)
	if !ok {
		return Mode{}, fmt.Errorf("%s の表示モードを取得できません", device)
	}
	return Mode{int(dm.PelsWidth), int(dm.PelsHeight), int(dm.DisplayFrequency)}, nil
}
