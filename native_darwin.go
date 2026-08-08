//go:build tray_native && darwin

package tray

// macOS system-tray backend: an NSStatusItem in the menu bar, driven through the
// Objective-C runtime via ebitengine/purego (CGO_ENABLED=0). This file is
// compile-verified in CI but its runtime behaviour must be confirmed on a real
// macOS session — a menu-bar item cannot be verified headlessly.
//
// Threading: AppKit is main-thread-only. Every NSStatusBar/NSStatusItem/NSMenu
// call therefore runs on the process main thread, marshalled there through
// -performSelectorOnMainThread: (see runOnMain). This matters most for Attach,
// which joins a host's already-running run loop: the goroutine that calls Attach
// is not pinned to the main OS thread, so issuing AppKit calls on it directly
// intermittently raised an Objective-C exception (→ SIGABRT) when the goroutine
// happened to be scheduled on a non-main thread. Marshalling removes that race
// for both the Attach (join) and Run (own-the-loop) paths.
//
// Menu clicks are dispatched back to MenuItem.Activate via a runtime-built target
// class whose action reads the clicked NSMenuItem's integer tag; that tag indexes
// the shared, unit-tested leafItems() table.

import (
	"runtime"
	"unsafe"

	objc "github.com/go-macos/objc"
)

const (
	nsVariableStatusItemLength             = -1.0
	nsApplicationActivationPolicyAccessory = 1
)

func sel(name string) objc.SEL { return objc.Sel(name) }

// class returns a class object as an ID so class messages (alloc, shared…) can
// be Send directly.
func class(name string) objc.ID { return objc.ClassID(name) }

// defaultBackend links the macOS NSStatusItem backend under -tags tray_native.
func defaultBackend() Backend { return newNativeBackend() }

// darwinBackend holds the live Objective-C objects and the flat item table the
// action handler dispatches through.
type darwinBackend struct {
	app       objc.ID
	statusBar objc.ID
	item      objc.ID
	items     []*MenuItem // tag -> item; sourced from the shared leafItems()
	tagSeq    int         // running leaf index while an NSMenu is being built
	targetCls objc.Class
	target    objc.ID
	pending   *Tray // tray the main-thread setup/refresh selectors act on
}

func newNativeBackend() Backend { return &darwinBackend{} }

// nsString builds an NSString from a Go string.
func nsString(s string) objc.ID { return objc.NSString(s) }

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

// prepare builds the click-dispatch target class and its instance (neither
// touches AppKit, so this is safe on whatever goroutine/thread called Run or
// Attach), then marshals the actual AppKit setup onto the main thread. Shared by
// Run and Attach; it does NOT touch the activation policy or start a run loop, so
// a host that owns those is unaffected.
func (b *darwinBackend) prepare(t *Tray) {
	b.pending = t

	// A target class whose -handle: reads the sender's tag and activates it, plus
	// two main-thread trampolines: -goTraySetup: creates the status item and
	// applies state, -goTrayRefresh: re-applies state after a change.
	b.targetCls, _ = objc.RegisterClass(
		"GoWidgetsTrayTarget",
		objc.GetClass("NSObject"),
		// MethodDef.Fn is the raw Go func; RegisterClass wraps it with NewIMP
		// itself (wrapping it here would make it re-wrap an IMP and panic).
		[]objc.MethodDef{
			{
				Cmd: sel("handle:"),
				Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID) {
					dispatchLeaf(b.items, int(sender.Send(sel("tag"))))
				},
			},
			{
				Cmd: sel("goTraySetup:"),
				Fn: func(self objc.ID, _cmd objc.SEL, _ objc.ID) {
					b.setupOnMain()
				},
			},
			{
				Cmd: sel("goTrayRefresh:"),
				Fn: func(self objc.ID, _cmd objc.SEL, _ objc.ID) {
					b.apply(b.pending)
				},
			},
		},
	)
	b.target = objc.ID(b.targetCls).Send(sel("alloc")).Send(sel("init"))

	// All AppKit work happens inside setupOnMain, on the main thread.
	b.runOnMain(sel("goTraySetup:"))
	t.ready()
}

// runOnMain performs selector s on the process main thread and blocks until it
// completes. On the main thread Cocoa runs it inline (so it also works before a
// run loop is started, i.e. the Run path); from any other thread it is queued on
// the host's running main run loop (the Attach path). A nil target — Refresh
// before prepare — makes it a safe Objective-C nil-message no-op.
func (b *darwinBackend) runOnMain(s objc.SEL) {
	b.target.Send(sel("performSelectorOnMainThread:withObject:waitUntilDone:"), s, objc.ID(0), true)
}

// setupOnMain resolves the shared NSApplication, creates the status-bar item and
// applies the tray state. It runs on the main thread (AppKit requirement) via
// runOnMain.
func (b *darwinBackend) setupOnMain() {
	b.app = class("NSApplication").Send(sel("sharedApplication"))
	b.statusBar = class("NSStatusBar").Send(sel("systemStatusBar"))
	b.item = b.statusBar.Send(sel("statusItemWithLength:"), nsVariableStatusItemLength)
	// statusItemWithLength: hands back an autoreleased reference; retain it so the
	// NSStatusItem lives for the whole process and can't be freed mid-use.
	b.item.Send(sel("retain"))
	b.apply(b.pending)
}

func (b *darwinBackend) Run(t *Tray) error {
	runtime.LockOSThread()
	b.prepare(t)
	// Run owns the whole process: it is a pure menu-bar app, so hide the dock
	// tile and start the AppKit run loop (blocks until Quit).
	b.app.Send(sel("setActivationPolicy:"), nsApplicationActivationPolicyAccessory)
	b.app.Send(sel("run"))
	return nil
}

// Attach adds the status item to the host's already-running NSApplication and
// returns immediately: no LockOSThread (the host owns its AppKit main thread), no
// activation-policy change (the host stays a Regular, dock-visible app), and no
// [NSApp run] (the host owns the loop). It is safe to call from any goroutine —
// the AppKit work is marshalled onto the main thread — but the host's main run
// loop must be running (or about to run) so the marshalled setup can execute.
func (b *darwinBackend) Attach(t *Tray) error {
	b.prepare(t)
	return nil
}

func (b *darwinBackend) Refresh(t *Tray) {
	b.pending = t
	b.runOnMain(sel("goTrayRefresh:"))
}

// apply pushes the tray's icon, tooltip and menu into the live NSStatusItem. It
// runs on the main thread (called only from setupOnMain / the goTrayRefresh:
// trampoline).
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
	// The click-dispatch table is the shared, unit-tested leafItems() order; the
	// per-item tags assigned in buildMenu index straight into it.
	b.items = leafItems(t.Menu())
	b.tagSeq = 0
	menu := b.buildMenu(t.Menu())
	b.item.Send(sel("setMenu:"), menu)
}

// buildMenu converts a *Menu into an NSMenu, tagging each actionable item with
// its leafItems index so the target's -handle: can find it. It walks the tree in
// the same depth-first pre-order as leafItems, so tagSeq stays in lock-step with
// b.items.
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
			tag := b.tagSeq
			b.tagSeq++
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
