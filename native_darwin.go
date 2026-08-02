//go:build tray_native && darwin

package tray

// macOS system-tray backend: an NSStatusItem in the menu bar, driven through the
// Objective-C runtime via ebitengine/purego (CGO_ENABLED=0). This file is
// compile-verified in CI but its runtime behaviour must be confirmed on a real
// macOS session — a menu-bar item cannot be verified headlessly.
//
// Threading: AppKit requires all UI work on the process's main OS thread and
// [NSApp run] blocks it, so Tray.Run must be called from main (this backend
// LockOSThread's the calling goroutine). Menu clicks are dispatched back to
// MenuItem.Activate via a runtime-built target class whose action reads the
// clicked NSMenuItem's integer tag.

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

const (
	nsVariableStatusItemLength             = -1.0
	nsApplicationActivationPolicyAccessory = 1
)

func sel(name string) objc.SEL { return objc.RegisterName(name) }

// class returns a class object as an ID so class messages (alloc, shared…) can
// be Send directly.
func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

// defaultBackend links the macOS NSStatusItem backend under -tags tray_native.
func defaultBackend() Backend { return newNativeBackend() }

// darwinBackend holds the live Objective-C objects and the flat item table the
// action handler dispatches through.
type darwinBackend struct {
	app       objc.ID
	statusBar objc.ID
	item      objc.ID
	items     []*MenuItem // indexed by NSMenuItem tag
	targetCls objc.Class
	target    objc.ID
}

func newNativeBackend() Backend { return &darwinBackend{} }

// nsString builds an NSString from a Go string.
func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	return class("NSString").Send(sel("stringWithUTF8String:"), &b[0])
}

// nsImageFromPNG builds an NSImage from PNG bytes (nil/empty → nil image).
func nsImageFromPNG(png []byte) objc.ID {
	if len(png) == 0 {
		return 0
	}
	data := class("NSData").Send(sel("dataWithBytes:length:"), unsafe.Pointer(&png[0]), uintptr(len(png)))
	img := class("NSImage").Send(sel("alloc")).Send(sel("initWithData:"), data)
	// a template image adapts to light/dark menu bars
	img.Send(sel("setTemplate:"), true)
	return img
}

func (b *darwinBackend) Run(t *Tray) error {
	runtime.LockOSThread()

	b.app = class("NSApplication").Send(sel("sharedApplication"))
	b.app.Send(sel("setActivationPolicy:"), nsApplicationActivationPolicyAccessory)

	// a target class whose -handle: reads the sender's tag and activates it
	b.targetCls, _ = objc.RegisterClass(
		"GoWidgetsTrayTarget",
		objc.GetClass("NSObject"),
		nil,
		nil,
		[]objc.MethodDef{{
			Cmd: sel("handle:"),
			Fn: objc.NewIMP(func(self objc.ID, _cmd objc.SEL, sender objc.ID) {
				tag := int(sender.Send(sel("tag")))
				if tag >= 0 && tag < len(b.items) {
					b.items[tag].Activate()
				}
			}),
		}},
	)
	b.target = objc.ID(b.targetCls).Send(sel("alloc")).Send(sel("init"))

	b.statusBar = class("NSStatusBar").Send(sel("systemStatusBar"))
	b.item = b.statusBar.Send(sel("statusItemWithLength:"), nsVariableStatusItemLength)

	b.apply(t)
	t.ready()
	b.app.Send(sel("run"))
	return nil
}

func (b *darwinBackend) Refresh(t *Tray) { b.apply(t) }

// apply pushes the tray's icon, tooltip and menu into the live NSStatusItem.
func (b *darwinBackend) apply(t *Tray) {
	if b.item == 0 {
		return
	}
	button := b.item.Send(sel("button"))
	if img := nsImageFromPNG(t.Icon()); img != 0 {
		button.Send(sel("setImage:"), img)
	}
	if tip := t.Tooltip(); tip != "" {
		button.Send(sel("setToolTip:"), nsString(tip))
	}
	b.items = b.items[:0]
	menu := b.buildMenu(t.Menu())
	b.item.Send(sel("setMenu:"), menu)
}

// buildMenu converts a *Menu into an NSMenu, tagging each actionable item so the
// target's -handle: can find it.
func (b *darwinBackend) buildMenu(m *Menu) objc.ID {
	menu := class("NSMenu").Send(sel("alloc")).Send(sel("init"))
	menu.Send(sel("setAutoenablesItems:"), false)
	for _, it := range m.Items {
		if it.Separator {
			menu.Send(sel("addItem:"), class("NSMenuItem").Send(sel("separatorItem")))
			continue
		}
		mi := class("NSMenuItem").Send(sel("alloc")).Send(sel("initWithTitle:action:keyEquivalent:"),
			nsString(it.Label), sel("handle:"), nsString(""))
		if it.Submenu != nil {
			mi.Send(sel("setSubmenu:"), b.buildMenu(it.Submenu))
		} else {
			tag := len(b.items)
			b.items = append(b.items, it)
			mi.Send(sel("setTag:"), tag)
			mi.Send(sel("setTarget:"), b.target)
			mi.Send(sel("setEnabled:"), !it.Disabled)
			if it.checkbox && it.Checked {
				mi.Send(sel("setState:"), 1) // NSControlStateValueOn
			}
		}
		menu.Send(sel("addItem:"), mi)
	}
	return menu
}

func (b *darwinBackend) Quit() {
	if b.app != 0 {
		b.app.Send(sel("stop:"), 0)
	}
}
