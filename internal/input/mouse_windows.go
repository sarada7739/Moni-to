//go:build windows

package input

import (
	"encoding/json"
	"log"
	"math"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	inputMouse = 0

	mouseMove        = 0x0001
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseWheel       = 0x0800
	mouseHWheel      = 0x1000
	mouseVirtualDesk = 0x4000
	mouseAbsolute    = 0x8000

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

// INPUT（type = INPUT_MOUSE）。64bit では 40 バイト。
type mouseInput struct {
	Type      uint32
	_         uint32
	Dx        int32
	Dy        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

func systemMetric(i uintptr) int32 {
	r, _, _ := procGetSystemMetrics.Call(i)
	return int32(r)
}

// virtualScreen は仮想デスクトップ全体の矩形を返す（DPI Aware なので物理ピクセル）。
func virtualScreen() Rect {
	x, y := systemMetric(smXVirtualScreen), systemMetric(smYVirtualScreen)
	return Rect{Left: x, Top: y, Right: x + systemMetric(smCXVirtualScreen), Bottom: y + systemMetric(smCYVirtualScreen)}
}

// Event はクライアントから DataChannel で届く操作イベント。
//
//	{"t":"move","x":0.5,"y":0.5}            カーソル移動（x,y は映像に対する 0〜1）
//	{"t":"down","b":"left","x":..,"y":..}   ボタン押下（b は left / right）
//	{"t":"up","b":"left","x":..,"y":..}     ボタン解放
//	{"t":"wheel","v":-120,"h":0}            スクロール（WHEEL_DELTA=120 単位。v>0 で上、h>0 で右）
type Event struct {
	T string  `json:"t"`
	X float64 `json:"x"`
	Y float64 `json:"y"`
	B string  `json:"b"`
	V int32   `json:"v"`
	H int32   `json:"h"`
}

// Injector は操作イベントを対象モニター上のマウス入力として送る。
type Injector struct {
	// Monitor は対象モニターの最新の矩形を返す（モニター構成の変化に追従するため毎回問い合わせる）。
	Monitor func() Rect

	mu       sync.Mutex
	mon      Rect
	monAt    time.Time
	pressed  map[string]bool
	warnedAt time.Time
}

// NewInjector は Injector を作る。
func NewInjector(monitor func() Rect) *Injector {
	return &Injector{Monitor: monitor, pressed: map[string]bool{}}
}

// HandleJSON は JSON 形式のイベントを処理する。
func (in *Injector) HandleJSON(b []byte) {
	var ev Event
	if err := json.Unmarshal(b, &ev); err != nil {
		return
	}
	in.Handle(ev)
}

// Handle はイベントを 1 つ処理する。
func (in *Injector) Handle(ev Event) {
	in.mu.Lock()
	defer in.mu.Unlock()
	switch ev.T {
	case "move":
		in.send(in.absMove(ev.X, ev.Y, 0))
	case "down", "up":
		flag, ok := buttonFlag(ev.B, ev.T == "down")
		if !ok {
			return
		}
		in.pressed[ev.B] = ev.T == "down"
		in.send(in.absMove(ev.X, ev.Y, flag))
	case "wheel":
		var batch []mouseInput
		if ev.V != 0 {
			batch = append(batch, mouseInput{Type: inputMouse, MouseData: uint32(clampWheel(ev.V)), Flags: mouseWheel})
		}
		if ev.H != 0 {
			batch = append(batch, mouseInput{Type: inputMouse, MouseData: uint32(clampWheel(ev.H)), Flags: mouseHWheel})
		}
		in.send(batch...)
	}
}

// ReleaseAll は押しっぱなしのボタンを離す（切断時にボタンが押されたまま残らないように）。
func (in *Injector) ReleaseAll() {
	in.mu.Lock()
	defer in.mu.Unlock()
	for b, down := range in.pressed {
		if !down {
			continue
		}
		if flag, ok := buttonFlag(b, false); ok {
			in.send(mouseInput{Type: inputMouse, Flags: flag})
		}
		in.pressed[b] = false
	}
}

func buttonFlag(b string, down bool) (uint32, bool) {
	switch {
	case b == "left" && down:
		return mouseLeftDown, true
	case b == "left":
		return mouseLeftUp, true
	case b == "right" && down:
		return mouseRightDown, true
	case b == "right":
		return mouseRightUp, true
	}
	return 0, false
}

func clampWheel(v int32) int32 {
	const lim = 120 * 20
	return int32(math.Max(-lim, math.Min(lim, float64(v))))
}

// absMove は対象モニター上の正規化座標への絶対移動（＋ボタン操作）を作る。
func (in *Injector) absMove(nx, ny float64, extra uint32) mouseInput {
	// モニター矩形は 1 秒キャッシュする（毎回 DXGI を列挙しないため）
	if time.Since(in.monAt) > time.Second {
		in.mon = in.Monitor()
		in.monAt = time.Now()
	}
	px, py := ToPixel(in.mon, nx, ny)
	vs := virtualScreen()
	return mouseInput{
		Type:  inputMouse,
		Dx:    ToAbsolute(px, vs.Left, vs.Width()),
		Dy:    ToAbsolute(py, vs.Top, vs.Height()),
		Flags: mouseMove | mouseAbsolute | mouseVirtualDesk | extra,
	}
}

func (in *Injector) send(inputs ...mouseInput) {
	if len(inputs) == 0 {
		return
	}
	n, _, err := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	// 注意：管理者権限のウィンドウへの入力は UIPI で破棄されるが、
	// その場合も戻り値では検出できないことがある（MSDN の記載どおり）
	if int(n) != len(inputs) && time.Since(in.warnedAt) > 10*time.Second {
		in.warnedAt = time.Now()
		log.Printf("[input] SendInput に失敗しました: %v", err)
	}
}
