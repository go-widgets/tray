//go:build tray_native && windows

package tray

// Windows system-tray backend: a Shell_NotifyIcon notification-area icon whose
// context menu is a TrackPopupMenu popup. CGO_ENABLED=0 throughout — the shared
// Win32 surface (the message-only owner window, the class registration, the
// GetMessage/DispatchMessage pump, DefWindowProc/PostQuitMessage/PostMessage,
// LoadCursor/GetModuleHandle) comes from github.com/go-mswin/win32; only the
// tray-specific procedures (Shell_NotifyIconW, the popup-menu and icon calls)
// are bound here, off win32's shared lazy DLL handles rather than re-declaring
// user32/gdi32/kernel32.
//
// This file is compile-verified in headless CI; the live notification icon and
// its menu are proven on the Win11 ARM64 QEMU VM.
//
// Threading: a message-only window (win32.MessageWindow, HWND_MESSAGE) owns the
// icon; its window procedure and win32.Pump both run on the goroutine that
// called Run, pinned with runtime.LockOSThread. The tray's callback message
// reports mouse events; a right/context click builds the popup menu on demand,
// and the chosen command id maps back to a *MenuItem.

import (
	"runtime"
	"unsafe"

	"github.com/go-mswin/win32"
	"golang.org/x/sys/windows"
)

// Tray-specific procedures, bound off win32's shared lazy DLL handles. The
// window/class/pump procedures the tray used to declare here now live in
// go-mswin/win32.
var (
	procSetForegroundWindow = win32.User32.NewProc("SetForegroundWindow")
	procGetCursorPos        = win32.User32.NewProc("GetCursorPos")
	procCreatePopupMenu     = win32.User32.NewProc("CreatePopupMenu")
	procAppendMenu          = win32.User32.NewProc("AppendMenuW")
	procTrackPopupMenu      = win32.User32.NewProc("TrackPopupMenu")
	procDestroyMenu         = win32.User32.NewProc("DestroyMenu")
	procDestroyIcon         = win32.User32.NewProc("DestroyIcon")
	procCreateIconIndirect  = win32.User32.NewProc("CreateIconIndirect")

	procShellNotifyIcon = win32.Shell32.NewProc("Shell_NotifyIconW")

	procCreateBitmap = win32.Gdi32.NewProc("CreateBitmap")
	procDeleteObject = win32.Gdi32.NewProc("DeleteObject")
)

// Tray-specific message and menu constants (the shared WM_*/IDC_* live in
// go-mswin/win32).
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
)

// notifyIconData mirrors Win32 NOTIFYICONDATAW (tray-specific).
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

// windowsBackend owns the message-only window and the live notification icon.
type windowsBackend struct {
	tray   *Tray
	mw     *win32.MessageWindow
	hIcon  windows.Handle
	nid    notifyIconData
	leaves []*MenuItem // command id (1-based) -> item, from leafItems
	nextID int
	added  bool
}

// defaultBackend links the Windows Shell_NotifyIcon backend under -tags tray_native.
func defaultBackend() Backend { return &windowsBackend{} }

func (b *windowsBackend) Run(t *Tray) error {
	runtime.LockOSThread()
	b.tray = t

	// A hidden HWND_MESSAGE window (class registration, cursor, module handle and
	// creation all handled by the shared helper) owns the notification icon.
	mw, err := win32.NewMessageWindow("GoWidgetsTrayWindow", win32.NewCallback(b.wndProc))
	if err != nil {
		return err
	}
	b.mw = mw

	b.apply(t)
	t.ready()

	if err := win32.Pump(); err != nil {
		return err
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
	if b.mw != nil && b.mw.Hwnd != 0 {
		win32.PostMessage(b.mw.Hwnd, wmClose, 0, 0)
	}
}

// wndProc is the message-only window's procedure. Its signature is all-uintptr
// so win32.NewCallback (windows.NewCallback) accepts it.
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
		win32.PostQuitMessage(0)
		return 0
	}
	return uintptr(win32.DefWindowProc(win32.HWND(hwnd), uint32(msgID), win32.WPARAM(wParam), win32.LPARAM(lParam)))
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

	var pt win32.Point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// SetForegroundWindow + a trailing WM_NULL is the documented workaround that
	// lets the popup dismiss correctly for a hidden owner window.
	procSetForegroundWindow.Call(uintptr(b.mw.Hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(
		hmenu,
		tpmReturnCmd|tpmRightButton,
		uintptr(pt.X), uintptr(pt.Y),
		0,
		uintptr(b.mw.Hwnd),
		0,
	)
	win32.PostMessage(b.mw.Hwnd, wmNull, 0, 0)
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
	ii := win32.IconInfo{FIcon: 1, HbmMask: win32.HBITMAP(hbmMask), HbmColor: win32.HBITMAP(hbmColor)}
	hicon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	procDeleteObject.Call(hbmColor)
	procDeleteObject.Call(hbmMask)
	return windows.Handle(hicon)
}

// apply pushes the tray's icon, tooltip and (leaf table for) menu into the live
// notification icon, adding it on first call and modifying it afterwards.
func (b *windowsBackend) apply(t *Tray) {
	if b.mw == nil || b.mw.Hwnd == 0 {
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
		hWnd:             windows.Handle(b.mw.Hwnd),
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
