//go:build darwin

package tray

// macOS system-tray backend: an NSStatusItem in the menu bar, driven through the
// Objective-C runtime via github.com/go-macos/objc — the fleet's shared purego
// bridge (CGO_ENABLED=0). Nothing here re-implements what that package already
// provides: no local class/selector helpers, no hand-written
// +[NSApplication sharedApplication], no hand-written [NSApp run].
//
// WHY THAT MATTERS, measured. This file used to look up NSApplication itself
// with its own one-line class() helper, and no line in this package ever loaded
// AppKit. The NSApplication class therefore did not exist in the process, the
// lookup returned the nil class, +sharedApplication returned nil, and every
// message after it returned zero — in SILENCE, because Objective-C does not
// complain about a message to nil. [NSApp run] returned at once. The observable
// was a menu-bar program that printed "godl is in the menu bar", exited 0 in
// 0.549s, showed nothing and logged nothing. Traces from that build read
// "isMainThread=1 app=0 item=0": the thread was right, the object was nil.
//
// Two things came out of it and both are load-bearing here. AppKit is loaded
// where NSApplication is NAMED (ensureAppKit, below) rather than left to the
// caller to remember. And every object AppKit hands back is CHECKED before it
// is used — see checkNative in native_shared.go — so that a nil arrives as an
// error that says which object was nil, not as a program that quietly does
// nothing.
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
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	objc "github.com/go-macos/objc"
)

const (
	nsVariableStatusItemLength             = -1.0
	nsApplicationActivationPolicyAccessory = 1
)

// appKitOnce loads AppKit (and Foundation) exactly once per process.
var (
	appKitOnce sync.Once
	appKitErr  error
)

// ensureAppKit dlopens the frameworks whose classes this file names, once.
//
// It is called before anything looks NSApplication up, because a class that has
// not been loaded is not "missing" in any way the runtime will mention: the
// lookup yields the nil class and every message to it yields zero. objc.App()
// carries the same guarantee from go-macos/objc v0.6.0 onwards; loading here
// too costs one refcounted dlopen of an already-resident framework and makes
// this package correct against the version it currently requires, which is the
// version the defect above was measured on.
func ensureAppKit() error {
	appKitOnce.Do(func() {
		if err := objc.Load(objc.AppKit, objc.Foundation); err != nil {
			appKitErr = fmt.Errorf("tray: loading AppKit: %w", err)
		}
	})
	return appKitErr
}

// targetClassSeq numbers the runtime target classes.
//
// objc_allocateClassPair REFUSES a duplicate class name and returns nil, so a
// process that builds a second tray would otherwise get a nil class, a nil
// target instance, and a menu whose every row has a nil target: it draws
// perfectly and answers no click. A per-backend name sidesteps that entirely
// and keeps each backend's own Go closures attached to its own class.
var targetClassSeq atomic.Uint64

// defaultBackend links the macOS NSStatusItem backend.
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
	// setupErr carries the outcome of setupOnMain back across the main-thread
	// hop. runOnMain waits for the selector to finish, so it is written and
	// read on either side of a rendezvous rather than concurrently.
	setupErr error
	// stopping is what Run watches and Quit sets, from any goroutine.
	//
	// It replaces -[NSApplication stop:], which sets a flag AppKit reads only
	// after processing an event: a menu-bar program with nobody touching it
	// waits for an event that never comes, and Run never returns.
	stopping atomic.Bool
	// lastMenuSig is the signature of the menu currently on screen, so an
	// icon-only refresh does not rebuild it. Written and read on the main
	// thread only, like the rest of the AppKit state above.
	lastMenuSig string
	// lastTitle is the button's title as of the last refresh, so a refresh
	// that hasn't changed it skips re-sending setTitle:. Measured: without
	// this guard, TestLiveRefreshingTheIconDoesNotGrowTheProcess's 8000
	// icon-only refreshes went from 68s to timing out past 60s — setting a
	// REAL NSStatusBarButton's title, unlike its image, appears to force
	// actual menu-bar layout/window-server work even when the string is
	// unchanged. An icon that animates several times a second while the
	// title stays put must not pay that on every frame.
	lastTitle string
}

func newNativeBackend() Backend { return &darwinBackend{} }

// menuBarPoints is how tall a menu-bar icon is drawn, in points. macOS draws
// its own menu extras at 18 in a 22-point bar; the remaining 4 points are the
// margin that makes a row of icons look like a row rather than a fence.
const menuBarPoints = 18

// menuItemPoints is how tall a row's icon is drawn, in points.
//
// SMALLER than the bar's 18, and that is the point of having a second
// constant. A menu row is laid out around its text, and AppKit's own rows --
// the Finder's "Open With", a share menu -- carry a 16-point glyph against a
// body-text baseline. An 18-point image in a row is taller than the text it
// labels, so it pushes the row's height up and the list stops looking like a
// list. 16 is the largest that still sits inside the line box, which is why it
// is not 14: a 14-point glyph reads as an afterthought beside 13-point text,
// and a stroked icon rasterised that small starts losing its thinner strokes
// altogether.
const menuItemPoints = 16

// nsImageFromPNG builds an NSImage from PNG bytes (nil/empty → nil image),
// drawn at points points tall.
//
// The height is a PARAMETER rather than the bar's constant because the two
// places this package puts an image are laid out around different things: the
// menu bar around a 22-point bar, a menu row around its text. See
// menuBarPoints and menuItemPoints.
func nsImageFromPNG(png []byte, points float64) objc.ID {
	if len(png) == 0 {
		return 0
	}
	data := objc.ClassID("NSData").Send(objc.Sel("dataWithBytes:length:"), unsafe.Pointer(&png[0]), uintptr(len(png)))
	img := objc.ClassID("NSImage").Send(objc.Sel("alloc")).Send(objc.Sel("initWithData:"), data)
	if img == 0 {
		return 0
	}
	// An NSImage built from data takes its size from the bitmap's PIXEL count:
	// a 36-pixel icon reports 36 points, and stands taller than the 22-point
	// bar it is asked to sit in, and a 36-pixel row icon dwarfs the label it
	// belongs to. Nobody sees a warning; the icon is simply bigger than its
	// neighbours, which is how this was reported.
	//
	// Saying the size explicitly is what turns the extra pixels into
	// resolution instead of dimensions: the bitmap stays as detailed as it was
	// and AppKit draws it at the height a menu bar expects. The width follows
	// the aspect ratio, so a caller shipping a wordmark is not squashed.
	if sz := objc.Send[objc.NSSize](img, objc.Sel("size")); sz.Height > 0 {
		img.Send(objc.Sel("setSize:"), objc.NSSize{
			Width:  sz.Width * points / sz.Height,
			Height: points,
		})
	}
	// A template image adapts to light/dark menu bars -- and is RECOLOURED to do
	// it, so an icon carrying colour must not be one or the colour is the first
	// thing lost. Decided from the picture: see IsTemplate.
	img.Send(objc.Sel("setTemplate:"), IsTemplate(png))
	return img
}

// prepare loads AppKit, builds the click-dispatch target class and its instance
// (neither touches AppKit's main-thread-only surface, so this is safe on
// whatever goroutine/thread called Run or Attach), then marshals the actual
// AppKit setup onto the main thread and reports what it found there. Shared by
// Run and Attach; it does NOT touch the activation policy or start a run loop,
// so a host that owns those is unaffected.
//
// ONCE per backend for the class and the target, like the status item itself: a
// program that runs the loop, stops it to show a window and runs it again calls
// this each time, and registering a fresh Objective-C class per call would grow
// the runtime's class table for the life of the process.
func (b *darwinBackend) prepare(t *Tray) error {
	b.pending = t

	if err := ensureAppKit(); err != nil {
		return err
	}

	if b.target == 0 {
		// A target class whose -handle: reads the sender's tag and activates it,
		// plus two main-thread trampolines: -goTraySetup: creates the status item
		// and applies state, -goTrayRefresh: re-applies state after a change.
		name := "GoWidgetsTrayTarget" + strconv.FormatUint(targetClassSeq.Add(1), 10)
		cls, err := objc.RegisterClass(
			name,
			objc.GetClass("NSObject"),
			// MethodDef.Fn is the raw Go func; RegisterClass wraps it with NewIMP
			// itself (wrapping it here would make it re-wrap an IMP and panic).
			[]objc.MethodDef{
				{
					Cmd: objc.Sel("handle:"),
					Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID) {
						dispatchLeaf(b.items, int(sender.Send(objc.Sel("tag"))))
					},
				},
				{
					Cmd: objc.Sel("goTraySetup:"),
					Fn: func(self objc.ID, _cmd objc.SEL, _ objc.ID) {
						b.setupOnMain()
					},
				},
				{
					Cmd: objc.Sel("goTrayRefresh:"),
					Fn: func(self objc.ID, _cmd objc.SEL, _ objc.ID) {
						// A pool of its own: this runs on the MAIN thread, so a
						// caller's pool does not cover it, and the autoreleased
						// NSData and NSString every refresh makes would sit until
						// the run loop happened to drain. An animated icon
						// refreshes several times a second, and the drain is not
						// on that schedule.
						objc.AutoreleasePool(func() { b.apply(b.pending) })
					},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNoTargetClass, err)
		}
		b.targetCls = cls
		b.target = objc.ID(b.targetCls).Send(objc.Sel("alloc")).Send(objc.Sel("init"))
		// Checked HERE and not only inside setupOnMain: the hop below is itself a
		// message to b.target, so a nil target would make it a silent no-op and
		// setupOnMain would never run to report anything at all.
		if b.target == 0 {
			return ErrNoTargetClass
		}
	}

	// All AppKit work happens inside setupOnMain, on the main thread.
	b.setupErr = nil
	b.runOnMain(objc.Sel("goTraySetup:"))
	if b.setupErr != nil {
		return b.setupErr
	}
	t.ready()
	return nil
}

// runOnMain performs selector s on the process main thread and blocks until it
// completes. On the main thread Cocoa runs it inline (so it also works before a
// run loop is started, i.e. the Run path); from any other thread it is queued on
// the host's running main run loop (the Attach path). A nil target — Refresh
// before prepare — makes it a safe Objective-C nil-message no-op.
func (b *darwinBackend) runOnMain(s objc.SEL) {
	b.target.Send(objc.Sel("performSelectorOnMainThread:withObject:waitUntilDone:"), s, objc.ID(0), true)
}

// setupOnMain resolves the shared NSApplication, creates the status-bar item and
// applies the tray state, recording in b.setupErr which object AppKit refused if
// any of them came back nil. It runs on the main thread (AppKit requirement) via
// runOnMain.
//
// ONE item, however often this runs. statusItemWithLength: makes a NEW item
// every time it is sent, and the one already in the bar is retained and does
// not go away, so a program that runs its tray, quits the loop to show a window
// and runs it again ends up with two icons in the menu bar and no way to get rid
// of either. That is a real sequence, not a hypothetical: go-xrkit/desk holds
// the loop while it waits for a headset, hands the main thread to its settings
// window, and takes the loop back afterwards -- and grew a second pair of
// glasses in the menu bar every time somebody pressed Save.
func (b *darwinBackend) setupOnMain() {
	b.app = objc.App()
	if b.statusBar == 0 {
		b.statusBar = objc.ClassID("NSStatusBar").Send(objc.Sel("systemStatusBar"))
	}
	// Guarded rather than chained: statusItemWithLength: sent to a nil status
	// bar would return nil too, and the error would then name the wrong object.
	made := false
	if b.item == 0 && b.statusBar != 0 {
		b.item = b.statusBar.Send(objc.Sel("statusItemWithLength:"), nsVariableStatusItemLength)
		made = true
	}
	if err := checkNative(uintptr(b.app), uintptr(b.statusBar), uintptr(b.item), uintptr(b.target)); err != nil {
		b.setupErr = err
		return
	}
	if made {
		// statusItemWithLength: hands back an autoreleased reference; retain it
		// so the NSStatusItem lives for the whole process and can't be freed
		// mid-use. Once, for the item that was just made: retaining the same
		// item on every run would be a leak in the other direction.
		b.item.Send(objc.Sel("retain"))
	}
	b.apply(b.pending)
}

// Run shows the tray and blocks on AppKit's run loop until Quit. It reports the
// setup failure rather than entering a loop that would return immediately: a
// [NSApp run] on a nil application is the defect this backend was rewritten for,
// and it is indistinguishable, from outside, from a clean exit.
func (b *darwinBackend) Run(t *Tray) error {
	runtime.LockOSThread()
	if err := b.prepare(t); err != nil {
		return err
	}
	// RunAppLoop rather than RunApp, because RunApp cannot be LEFT.
	// -[NSApplication run] reads stop:'s flag only after it has processed an
	// event, so a program nobody is touching waits for an event that never
	// comes and Run never returns. RunAppLoop is the same loop with the caller
	// asked between events and a bounded wait, so the question gets asked
	// whether or not anything happens.
	//
	// The flag is cleared as Run RETURNS, not as it starts. This tray is run,
	// released and run again -- a desk holds the loop while it waits for a
	// headset and hands it back when it finds one -- and a Quit that arrives
	// before Run has entered the loop must not be forgotten. Clearing on the
	// way in did exactly that, and it is the shape the defect takes when the
	// thing being waited for is ALREADY there.
	defer b.stopping.Store(false)
	objc.RunAppLoop(nsApplicationActivationPolicyAccessory, b.stopping.Load)
	return nil
}

// Attach adds the status item to the host's already-running NSApplication and
// returns immediately: no LockOSThread (the host owns its AppKit main thread), no
// activation-policy change (the host stays a Regular, dock-visible app), and no
// [NSApp run] (the host owns the loop). It is safe to call from any goroutine —
// the AppKit work is marshalled onto the main thread — but the host's main run
// loop must be running (or about to run) so the marshalled setup can execute.
func (b *darwinBackend) Attach(t *Tray) error {
	return b.prepare(t)
}

func (b *darwinBackend) Refresh(t *Tray) {
	b.pending = t
	b.runOnMain(objc.Sel("goTrayRefresh:"))
}

// apply pushes the tray's icon, tooltip and menu into the live NSStatusItem. It
// runs on the main thread (called only from setupOnMain / the goTrayRefresh:
// trampoline).
func (b *darwinBackend) apply(t *Tray) {
	if b.item == 0 {
		return
	}
	button := b.item.Send(objc.Sel("button"))
	if img := nsImageFromPNG(t.Icon(), menuBarPoints); img != 0 {
		button.Send(objc.Sel("setImage:"), img)
		// setImage: retains, so the reference alloc gave us is ours to drop.
		// Without this every refresh leaks an NSImage and its bitmap. That was
		// survivable while the icon changed twice a minute and stops being so
		// the moment one animates: a caller redrawing five times a second
		// leaks eighteen thousand images an hour.
		img.Send(objc.Sel("release"))
	}
	if tip := t.Tooltip(); tip != "" {
		button.Send(objc.Sel("setToolTip:"), objc.NSString(tip))
	}
	// Guarded by lastTitle (see its own doc comment) rather than sent
	// unconditionally like setImage: — but still sent whenever the title
	// actually differs, INCLUDING going back to "" (an account removed, a
	// metric that stopped applying), so the button never keeps showing a
	// stale title next to a now-unrelated icon.
	if title := t.Title(); title != b.lastTitle {
		button.Send(objc.Sel("setTitle:"), objc.NSString(title))
		b.lastTitle = title
	}
	// The click-dispatch table is the shared, unit-tested leafItems() order; the
	// per-item tags assigned in buildMenu index straight into it. It is rebuilt
	// every time because it is cheap and it carries the CURRENT handlers, which
	// the signature deliberately ignores.
	b.items = leafItems(t.Menu())

	// Rebuild the platform menu only when what it draws has changed.
	//
	// This used to run on every refresh. That was affordable while the icon
	// changed twice a minute and stops being so the moment one animates: an
	// NSMenu and an NSMenuItem per row, several times a second, none of them
	// released — and worse, the menu replaced under a user who has it open.
	if sig := menuSignature(t.Menu()); sig != b.lastMenuSig {
		b.tagSeq = 0
		menu := b.buildMenu(t.Menu())
		b.item.Send(objc.Sel("setMenu:"), menu)
		// setMenu: retains; the reference alloc gave us is ours to drop.
		menu.Send(objc.Sel("release"))
		b.lastMenuSig = sig
	}
}

// buildMenu converts a *Menu into an NSMenu, tagging each actionable item with
// its leafItems index so the target's -handle: can find it. It walks the tree in
// the same depth-first pre-order as leafItems, so tagSeq stays in lock-step with
// b.items.
func (b *darwinBackend) buildMenu(m *Menu) objc.ID {
	menu := objc.ClassID("NSMenu").Send(objc.Sel("alloc")).Send(objc.Sel("init"))
	menu.Send(objc.Sel("setAutoenablesItems:"), false)
	for _, it := range m.Items {
		if it.Separator {
			menu.Send(objc.Sel("addItem:"), objc.ClassID("NSMenuItem").Send(objc.Sel("separatorItem")))
			continue
		}
		mi := objc.ClassID("NSMenuItem").Send(objc.Sel("alloc")).Send(objc.Sel("initWithTitle:action:keyEquivalent:"),
			objc.NSString(it.Label), objc.Sel("handle:"), objc.NSString(""))
		if img := nsImageFromPNG(it.Icon, menuItemPoints); img != 0 {
			mi.Send(objc.Sel("setImage:"), img)
			// setImage: retains, so the reference alloc gave us is ours to
			// drop -- the same ownership rule as the bar's image, and it bites
			// harder here: the bar has ONE image and a menu has one per row,
			// stranded on every rebuild. Measured on this package's own
			// refresh loop: 4000 rebuilds cost +31 MB with the reference kept
			// against +7.5 MB with it released.
			img.Send(objc.Sel("release"))
		}
		if it.Submenu != nil {
			sub := b.buildMenu(it.Submenu)
			mi.Send(objc.Sel("setSubmenu:"), sub)
			sub.Send(objc.Sel("release")) // setSubmenu: retains
		} else {
			tag := b.tagSeq
			b.tagSeq++
			mi.Send(objc.Sel("setTag:"), tag)
			mi.Send(objc.Sel("setTarget:"), b.target)
			mi.Send(objc.Sel("setEnabled:"), !it.Disabled)
			if it.checkbox && it.Checked {
				mi.Send(objc.Sel("setState:"), 1) // NSControlStateValueOn
			}
		}
		menu.Send(objc.Sel("addItem:"), mi)
		mi.Send(objc.Sel("release")) // addItem: retains
	}
	return menu
}

// Quit asks the run loop Run entered to return, and WAKES it so that it does.
//
// -[NSApplication stop:] only sets a flag, which AppKit reads after it finishes
// processing an event. A menu-bar program with nobody touching it is sitting in
// -nextEventMatchingMask: waiting for an event that is never coming, so the
// flag is never read and Run never returns. Measured: a desk that runs this
// loop while it waits for a headset found one, called Quit, and hung at 0% CPU
// until somebody moved the mouse -- in a program whose whole point is that it
// starts by itself.
//
// objc.StopApp sets the flag and then stops the main thread's run loop, which
// makes the event wait return, which lets AppKit read the flag it was given.
func (b *darwinBackend) Quit() {
	b.stopping.Store(true)
	// And WAKE the wait, or the question is not asked until an event happens to
	// arrive -- which, in a program nobody is touching, is never.
	objc.WakeMainRunLoop()
}
