package tray

// Windows notifications: a Shell_NotifyIcon balloon (NIF_INFO) on the tray's own
// notification-area icon. On Windows 10/11 the shell renders the balloon as a
// toast, so this is a real system notification without a WinRT dependency (a
// native toast needs an AppUserModelID shortcut in the Start menu and COM; the
// balloon needs neither).

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Notify implements [Notifier]. The icon must be up (Run or Attach returned).
func (b *windowsBackend) Notify(_ *Tray, n Notification) error {
	b.nmu.Lock()
	defer b.nmu.Unlock()
	if !b.added || b.nid.hWnd == 0 {
		return ErrNotRunning
	}
	title, info := balloonUnits(n)
	nid := b.nid
	nid.uFlags |= nifInfo
	nid.dwInfoFlags = niifInfo
	nid.szInfoTitle = [64]uint16{}
	nid.szInfo = [256]uint16{}
	copy(nid.szInfoTitle[:], title)
	copy(nid.szInfo[:], info)
	r, _, err := procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		if err == nil || errors.Is(err, windows.Errno(0)) {
			err = errors.New("Shell_NotifyIcon refused the balloon")
		}
		return err
	}
	b.click = n.OnClick
	return nil
}

// balloonEnded settles the latest balloon: clicked runs its OnClick (on a
// goroutine, so a slow callback does not stall the message pump), anything else
// forgets it.
func (b *windowsBackend) balloonEnded(clicked bool) {
	b.nmu.Lock()
	fn := b.click
	b.click = nil
	b.nmu.Unlock()
	if clicked && fn != nil {
		go fn()
	}
}
