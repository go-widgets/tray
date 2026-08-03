//go:build tray_native && windows

package tray

// Windows system-tray backend: a Shell_NotifyIcon notification-area icon whose
// context menu is a TrackPopupMenu popup. CGO_ENABLED=0 throughout — every
// Win32 entry point is reached through golang.org/x/sys/windows lazy procs.
//
// This file is compile-verified only. A notification-area icon needs a live
// Windows desktop session to confirm at runtime, which headless CI cannot do.
//
// Threading: a message-only window (HWND_MESSAGE) owns the icon; its window
// procedure and the GetMessage/DispatchMessage pump both run on the goroutine
// that called Run, which is pinned with runtime.LockOSThread. The tray's
// callback message reports mouse events; a right/context click builds the popup
// menu on demand, and the chosen command id maps back to a *MenuItem.

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procGetMessage          = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessage         = user32.NewProc("PostMessageW")
	procLoadCursor          = user32.NewProc("LoadCursorW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenu          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procCreateIconIndirect  = user32.NewProc("CreateIconIndirect")

	procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")

	procCreateBitmap = gdi32.NewProc("CreateBitmap")
	procDeleteObject = gdi32.NewProc("DeleteObject")

	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmCommand      = 0x0111
	wmNull         = 0x0000
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205
	wmContextMenu  = 0x007B
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1

	nimAdd    = 0x0
	nimModify = 0x1
	nimDelete = 0x2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	mfString    = 0x0
	mfGrayed    = 0x1
	mfChecked   = 0x8
	mfPopup     = 0x10
	mfSeparator = 0x800

	tpmReturnCmd   = 0x100
	tpmRightButton = 0x2

	idcArrow = 32512
)

// hwndMessage is HWND_MESSAGE ((HWND)-3): a parent that makes CreateWindowEx
// produce a message-only window (no taskbar/z-order presence).
const hwndMessage = ^uintptr(2)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             windows.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            windows.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     windows.Handle
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

// windowsBackend owns the message-only window and the live notification icon.
type windowsBackend struct {
	tray      *Tray
	hwnd      windows.Handle
	hInst     windows.Handle
	hIcon     windows.Handle
	nid       notifyIconData
	leaves    []*MenuItem // command id (1-based) -> item, from leafItems
	nextID    int
	className *uint16
	added     bool
}

// defaultBackend links the Windows Shell_NotifyIcon backend under -tags tray_native.
func defaultBackend() Backend { return &windowsBackend{} }

func (b *windowsBackend) Run(t *Tray) error {
	runtime.LockOSThread()
	b.tray = t

	h, _, _ := procGetModuleHandle.Call(0)
	b.hInst = windows.Handle(h)

	var err error
	if b.className, err = windows.UTF16PtrFromString("GoWidgetsTrayWindow"); err != nil {
		return err
	}
	cursor, _, _ := procLoadCursor.Call(0, idcArrow)

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   windows.NewCallback(b.wndProc),
		hInstance:     b.hInst,
		hCursor:       windows.Handle(cursor),
		lpszClassName: b.className,
	}
	if atom, _, e := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return e
	}

	hwnd, _, e := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(b.className)),
		uintptr(unsafe.Pointer(b.className)),
		0, 0, 0, 0, 0,
		hwndMessage,
		0,
		uintptr(b.hInst),
		0,
	)
	if hwnd == 0 {
		return e
	}
	b.hwnd = windows.Handle(hwnd)

	b.apply(t)
	t.ready()

	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT, -1 = error
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}

	nid := b.nid
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	if b.hIcon != 0 {
		procDestroyIcon.Call(uintptr(b.hIcon))
	}
	return nil
}

func (b *windowsBackend) Refresh(t *Tray) { b.apply(t) }

func (b *windowsBackend) Quit() {
	if b.hwnd != 0 {
		procPostMessage.Call(uintptr(b.hwnd), wmClose, 0, 0)
	}
}

// wndProc is the message-only window's procedure. Its signature is all-uintptr
// so windows.NewCallback accepts it.
func (b *windowsBackend) wndProc(hwnd, msgID, wParam, lParam uintptr) uintptr {
	switch uint32(msgID) {
	case wmTrayCallback:
		// For a legacy (pre-v4) icon the low word of lParam is the mouse message.
		switch uint32(lParam) & 0xffff {
		case wmRButtonUp, wmContextMenu, wmLButtonUp:
			b.showMenu()
		}
		return 0
	case wmCommand:
		// Only reached if a menu is tracked without TPM_RETURNCMD; kept as a
		// defensive dispatch path.
		b.dispatch(int(wParam & 0xffff))
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msgID, wParam, lParam)
	return r
}

// dispatch activates the leaf mapped to a 1-based command id.
func (b *windowsBackend) dispatch(id int) {
	if id >= 1 && id <= len(b.leaves) {
		b.leaves[id-1].Activate()
	}
}

// showMenu builds and tracks the popup menu at the cursor, then activates the
// chosen item (TrackPopupMenu with TPM_RETURNCMD returns the command id).
func (b *windowsBackend) showMenu() {
	menu := b.tray.Menu()
	b.leaves = leafItems(menu)
	b.nextID = 0
	hmenu := b.buildMenu(menu)
	defer procDestroyMenu.Call(hmenu)

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// SetForegroundWindow + a trailing WM_NULL is the documented workaround that
	// lets the popup dismiss correctly for a hidden owner window.
	procSetForegroundWindow.Call(uintptr(b.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(
		hmenu,
		tpmReturnCmd|tpmRightButton,
		uintptr(pt.x), uintptr(pt.y),
		0,
		uintptr(b.hwnd),
		0,
	)
	procPostMessage.Call(uintptr(b.hwnd), wmNull, 0, 0)
	if cmd != 0 {
		b.dispatch(int(cmd))
	}
}

// buildMenu creates an HMENU for m, assigning each leaf a 1-based command id in
// leafItems (depth-first) order so it matches b.leaves.
func (b *windowsBackend) buildMenu(m *Menu) uintptr {
	hmenu, _, _ := procCreatePopupMenu.Call()
	for _, it := range m.Items {
		switch {
		case it.Separator:
			procAppendMenu.Call(hmenu, mfSeparator, 0, 0)
		case it.Submenu != nil:
			sub := b.buildMenu(it.Submenu)
			label, _ := windows.UTF16PtrFromString(it.Label)
			procAppendMenu.Call(hmenu, mfString|mfPopup, sub, uintptr(unsafe.Pointer(label)))
		default:
			b.nextID++
			flags := uintptr(mfString)
			if it.Disabled {
				flags |= mfGrayed
			}
			if it.checkbox && it.Checked {
				flags |= mfChecked
			}
			label, _ := windows.UTF16PtrFromString(it.Label)
			procAppendMenu.Call(hmenu, flags, uintptr(b.nextID), uintptr(unsafe.Pointer(label)))
		}
	}
	return hmenu
}

// makeIcon turns PNG bytes into an HICON (0 when there is no icon).
func (b *windowsBackend) makeIcon(png []byte) windows.Handle {
	rgba, w, h, err := pngPixels(png)
	if err != nil || w == 0 || h == 0 {
		return 0
	}
	bgra := toBGRA(rgba)
	hbmColor, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 32, uintptr(unsafe.Pointer(&bgra[0])))
	// A 1-bpp AND mask, all zero: the color bitmap's alpha channel does the work.
	rowBytes := ((w + 15) / 16) * 2 // WORD-aligned scanlines
	mask := make([]byte, rowBytes*h)
	hbmMask, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	ii := iconInfo{fIcon: 1, hbmMask: windows.Handle(hbmMask), hbmColor: windows.Handle(hbmColor)}
	hicon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	procDeleteObject.Call(hbmColor)
	procDeleteObject.Call(hbmMask)
	return windows.Handle(hicon)
}

// apply pushes the tray's icon, tooltip and (leaf table for) menu into the live
// notification icon, adding it on first call and modifying it afterwards.
func (b *windowsBackend) apply(t *Tray) {
	if b.hwnd == 0 {
		return
	}
	b.leaves = leafItems(t.Menu())

	if b.hIcon != 0 {
		procDestroyIcon.Call(uintptr(b.hIcon))
		b.hIcon = 0
	}
	b.hIcon = b.makeIcon(t.Icon())

	b.nid = notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             b.hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayCallback,
		hIcon:            b.hIcon,
	}
	tip := windows.StringToUTF16(t.Tooltip())
	if len(tip) > len(b.nid.szTip) {
		tip = tip[:len(b.nid.szTip)]
		tip[len(tip)-1] = 0
	}
	copy(b.nid.szTip[:], tip)

	action := uintptr(nimModify)
	if !b.added {
		action = nimAdd
		b.added = true
	}
	procShellNotifyIcon.Call(action, uintptr(unsafe.Pointer(&b.nid)))
}
