//go:build darwin

package tray

// The LIVE macOS suite. It really puts a status item in the menu bar of the
// session it runs in and READS ITS PROPERTIES BACK.
//
// It exists because of what the defect it guards against looked like: the
// backend created an NSStatusItem out of a nil NSApplication, every AppKit call
// answered zero without complaint, and the program exited 0 having drawn
// nothing. Nothing short of reading a value back tells that apart from success —
// "it compiled", "it did not crash" and "the command ran" were all true of the
// broken build.
//
// It skips
// itself when there is no window server, because a session with no menu bar has
// nothing to put an item in and failing there would be failing for the wrong
// reason. Everything it creates, it leaves to the process exit — a test binary
// that lives for a second is the whole exposure.

import (
	"bytes"
	"image"
	"image/color"
	pngenc "image/png"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	objc "github.com/go-macos/objc"
)

const (
	// NSEventMaskAny is NSUIntegerMax.
	nsEventMaskAny = ^uint64(0)
	// The pump's per-turn deadline. It bounds how long a main-thread hop waits.
	pumpSeconds = 0.02
	// A live AppKit suite that hangs is the worst failure mode there is: the job
	// burns its whole budget and reports nothing, which is indistinguishable
	// from a broken runner.
	watchdog = 2 * time.Minute
)

// windowServer is decided once, in TestMain, before AppKit is touched.
var windowServer bool

// hasWindowServer reports whether this process can reach a window server, by
// asking whether AppKit can hand back a status bar at all. It is deliberately
// asked AFTER AppKit is loaded and an application object exists, because
// +[NSStatusBar systemStatusBar] is meaningless before both.
func hasWindowServer() bool {
	return objc.ClassID("NSStatusBar").Send(objc.Sel("systemStatusBar")) != 0
}

// TestMain pins the process main OS thread, creates the shared NSApplication on
// it the way a host application would, and then spends its life pumping the main
// thread while the tests run on another goroutine.
//
// The pump is the point. prepare() marshals its AppKit work onto the main thread
// with -performSelectorOnMainThread:waitUntilDone:YES, and that queue is
// serviced by a run loop and by nothing else; without a pump here the first test
// would block for ever. It is a bounded -nextEventMatchingMask: loop rather than
// [NSApp run] so TestMain can RETURN with the test exit code instead of calling
// os.Exit from inside a run loop.
func TestMain(m *testing.M) {
	runtime.LockOSThread()

	if err := ensureAppKit(); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	app := objc.App()
	if app == 0 {
		os.Stderr.WriteString(ErrNoApplication.Error() + "\n")
		os.Exit(1)
	}
	windowServer = hasWindowServer()
	if !windowServer {
		os.Exit(m.Run())
	}
	app.Send(objc.Sel("setActivationPolicy:"), nsApplicationActivationPolicyAccessory)
	app.Send(objc.Sel("finishLaunching"))

	go func() {
		time.Sleep(watchdog)
		os.Stderr.WriteString("live suite watchdog fired: the main-thread pump or a hop is stuck\n")
		os.Exit(1)
	}()

	result := make(chan int, 1)
	go func() { result <- m.Run() }()
	for {
		select {
		case code := <-result:
			os.Exit(code)
		default:
		}
		pumpOnce(app)
	}
}

// pumpOnce runs the main run loop for one short turn, delivering any event and
// servicing the -performSelectorOnMainThread: queue prepare() hops through.
func pumpOnce(app objc.ID) {
	objc.AutoreleasePool(func() {
		until := objc.ClassID("NSDate").Send(objc.Sel("dateWithTimeIntervalSinceNow:"), pumpSeconds)
		ev := app.Send(objc.Sel("nextEventMatchingMask:untilDate:inMode:dequeue:"),
			nsEventMaskAny, until, objc.NSString("kCFRunLoopDefaultMode"), true)
		if ev != 0 {
			app.Send(objc.Sel("sendEvent:"), ev)
		}
	})
}

func requireWindowServer(t *testing.T) {
	t.Helper()
	if !windowServer {
		t.Skip("no window server in this session: there is no menu bar to test against")
	}
}

// The test's own main-thread trampoline: a runtime class whose one method pops
// a closure off testHopQ and runs it. AppKit properties are READ through it
// rather than off the test goroutine, for exactly the reason the package WRITES
// them through one — a property read from a thread that is not the main one is
// undefined, and undefined here means "usually right".
var (
	testHopQ       = make(chan func(), 8)
	testTargetOnce sync.Once
	testTarget     objc.ID
	testTargetErr  error
)

func hopTarget() (objc.ID, error) {
	testTargetOnce.Do(func() {
		cls, err := objc.RegisterClass("GoWidgetsTrayTestHop", objc.GetClass("NSObject"),
			[]objc.MethodDef{{
				Cmd: objc.Sel("goTrayTestHop:"),
				Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
					select {
					case fn := <-testHopQ:
						fn()
					default:
					}
				},
			}})
		if err != nil {
			testTargetErr = err
			return
		}
		testTarget = objc.ID(cls).Send(objc.Sel("alloc")).Send(objc.Sel("init"))
		if testTarget == 0 {
			testTargetErr = ErrNoTargetClass
		}
	})
	return testTarget, testTargetErr
}

// onMain runs fn on the process main thread and returns once it has finished.
// waitUntilDone: is YES, so the send itself is the rendezvous; the watchdog in
// TestMain is what bounds a main thread that has stopped pumping.
func onMain(t *testing.T, fn func()) {
	t.Helper()
	target, err := hopTarget()
	if err != nil {
		t.Fatalf("registering the test hop class: %v", err)
	}
	done := make(chan struct{})
	testHopQ <- func() { fn(); close(done) }
	target.Send(objc.Sel("performSelectorOnMainThread:withObject:waitUntilDone:"),
		objc.Sel("goTrayTestHop:"), objc.ID(0), true)
	<-done
}

// TestLiveAttachPutsARealItemInTheMenuBar is the measurement the fix is claimed
// on. It asserts the two values the broken build reported as zero — the
// application and the status item — and then the one that distinguishes an item
// AppKit merely ALLOCATED from one it actually PLACED: the button's window.
func TestLiveAttachPutsARealItemInTheMenuBar(t *testing.T) {
	requireWindowServer(t)

	b := &darwinBackend{}
	tr := New(nil).WithBackend(b).SetTooltip("live test")
	tr.SetMenu(NewMenu().Add(Item("Quit", func() {})))
	if err := b.Attach(tr); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	var (
		app, item, button, window objc.ID
		tooltip                   string
		rows                      int
	)
	onMain(t, func() {
		app, item = b.app, b.item
		if item != 0 {
			button = item.Send(objc.Sel("button"))
			if button != 0 {
				window = button.Send(objc.Sel("window"))
				tooltip = objc.GoString(button.Send(objc.Sel("toolTip")))
			}
			if menu := item.Send(objc.Sel("menu")); menu != 0 {
				rows = int(menu.Send(objc.Sel("numberOfItems")))
			}
		}
	})

	// The two values the defect reported as zero, in the same shape the broken
	// build's traces printed them.
	t.Logf("app=%#x item=%#x button=%#x window=%#x", uintptr(app), uintptr(item), uintptr(button), uintptr(window))
	if app == 0 {
		t.Fatal("app is nil: AppKit is still not loaded where NSApplication is named")
	}
	if item == 0 {
		t.Fatal("item is nil: no NSStatusItem was created")
	}
	if button == 0 {
		t.Fatal("the status item has no button")
	}
	// The one that cannot be faked by allocation: an item AppKit refused to place
	// has no window, and nothing else about it differs.
	if window == 0 {
		t.Error("the status item has no window: it was created but never placed in the menu bar")
	}
	// Properties read BACK out of AppKit, which is what tells a configured item
	// apart from a merely allocated one.
	if tooltip != "live test" {
		t.Errorf("the button's tooltip reads %q, want %q", tooltip, "live test")
	}
	if rows != 1 {
		t.Errorf("the item's menu has %d rows, want 1", rows)
	}

	// The negative control for every non-nil check above: a class that does not
	// exist yields the nil class, and a message to nil yields nil. If the
	// lookups above "succeeded" vacuously, this would come back non-nil too.
	var bogus objc.ID
	onMain(t, func() {
		bogus = objc.ClassID("NSStatusBarThereIsNoSuchClass").Send(objc.Sel("systemStatusBar"))
	})
	if bogus != 0 {
		t.Error("a nonexistent class answered systemStatusBar; the checks above prove nothing")
	}
}

// TestLiveClickDispatchReachesTheGoHandler chooses a row through AppKit's own
// dispatch, so the registered runtime class, the NSMenuItem tag and the Go table
// are all exercised — not a Go function called directly.
func TestLiveClickDispatchReachesTheGoHandler(t *testing.T) {
	requireWindowServer(t)

	b := &darwinBackend{}
	ran := make(chan string, 2)
	tr := New(nil).WithBackend(b)
	tr.SetMenu(NewMenu().Add(
		&MenuItem{Label: "heading", Disabled: true},
		Separator(),
		Item("Pause", func() { ran <- "pause" }),
	))
	if err := b.Attach(tr); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	onMain(t, func() {
		b.item.Send(objc.Sel("menu")).Send(objc.Sel("performActionForItemAtIndex:"), 2)
	})
	select {
	case got := <-ran:
		if got != "pause" {
			t.Errorf("row 2 ran %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("choosing the row ran no handler")
	}

	// The negative controls: the heading is disabled and row 1 is a separator,
	// so AppKit must dispatch nothing for either.
	onMain(t, func() {
		b.item.Send(objc.Sel("menu")).Send(objc.Sel("performActionForItemAtIndex:"), 0)
		b.item.Send(objc.Sel("menu")).Send(objc.Sel("performActionForItemAtIndex:"), 1)
	})
	select {
	case got := <-ran:
		t.Errorf("a disabled row or a separator ran %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestLiveTwoBackendsEachGetTheirOwnTargetClass covers the reason the target
// class name carries a sequence number: objc_allocateClassPair refuses a
// duplicate name and returns nil, and a menu whose rows have a nil target draws
// perfectly and answers no click.
func TestLiveTwoBackendsEachGetTheirOwnTargetClass(t *testing.T) {
	requireWindowServer(t)

	a, c := &darwinBackend{}, &darwinBackend{}
	for _, b := range []*darwinBackend{a, c} {
		tr := New(nil).WithBackend(b)
		tr.SetMenu(NewMenu().Add(Item("x", func() {})))
		if err := b.Attach(tr); err != nil {
			t.Fatalf("Attach: %v", err)
		}
	}
	if a.targetCls == 0 || c.targetCls == 0 {
		t.Fatalf("target classes %#x and %#x: one of them is nil", uintptr(a.targetCls), uintptr(c.targetCls))
	}
	if a.targetCls == c.targetCls {
		t.Errorf("both backends share class %#x; the second registration must have been refused",
			uintptr(a.targetCls))
	}
	if a.target == 0 || c.target == 0 {
		t.Errorf("target instances %#x and %#x: one of them is nil", uintptr(a.target), uintptr(c.target))
	}
}

// TestLiveImageIsSizedForTheMenuBar reads the NSImage's size back out of AppKit.
//
// The defect it guards is quiet in the same way as the nil application: the icon
// is built, it is placed, everything answers, and it is simply TALLER than every
// neighbouring menu extra — because an NSImage built from data reports its
// PIXEL count as points, and the icon this ships is 36 pixels for a 22-point
// bar. No API says no. It was reported by someone looking at their menu bar.
func TestLiveImageIsSizedForTheMenuBar(t *testing.T) {
	requireWindowServer(t)
	runtime.LockOSThread()
	if objc.App() == 0 {
		t.Skip("no shared application in this session")
	}

	// A 36x36 PNG, which is what the fleet's generator emits: two pixels of a
	// black glyph is enough, the size is the whole subject.
	png := tallPNG(t, 36, 36)

	img := nsImageFromPNG(png, menuBarPoints)
	if img == 0 {
		t.Fatal("nsImageFromPNG returned a nil image for a valid PNG")
	}
	got := objc.Send[objc.NSSize](img, objc.Sel("size"))
	if got.Height != menuBarPoints {
		t.Errorf("image is %.1f points tall, want %d — a 22-point bar cannot hold it",
			got.Height, menuBarPoints)
	}
	if got.Width != menuBarPoints {
		t.Errorf("a square image came back %.1f x %.1f: the aspect ratio was not kept",
			got.Width, got.Height)
	}

	// The negative control: without this the test would pass on any image whose
	// pixels happened to be 18, and prove nothing about the sizing.
	raw := objc.ClassID("NSImage").Send(objc.Sel("alloc")).
		Send(objc.Sel("initWithData:"), objc.ClassID("NSData").
			Send(objc.Sel("dataWithBytes:length:"), unsafe.Pointer(&png[0]), uintptr(len(png))))
	if before := objc.Send[objc.NSSize](raw, objc.Sel("size")); before.Height != 36 {
		t.Fatalf("control: an unsized NSImage reported %.1f points, want 36 — "+
			"if AppKit no longer does this, the fix above is guarding nothing", before.Height)
	}
}

// tallPNG encodes a w x h PNG with one opaque pixel, so the bytes are a real
// image without the test carrying a fixture.
func tallPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	m.Set(w/2, h/2, color.NRGBA{A: 255})
	var b bytes.Buffer
	if err := pngenc.Encode(&b, m); err != nil {
		t.Fatalf("encode the probe image: %v", err)
	}
	return b.Bytes()
}

// TestLiveRefreshingTheIconDoesNotGrowTheProcess measures the thing a leak
// actually is: memory that goes up and does not come down.
//
// -[NSImage alloc] hands over a reference the caller owns and setImage: takes
// its own, so keeping ours strands an NSImage and its bitmap on every refresh.
// It is invisible — nothing fails, and it is kilobytes at a time — until an
// animated icon turns it into eighteen thousand images an hour.
//
// retainCount is NOT the instrument for this, though it is the obvious one. It
// counts pending autoreleases too, so it reports the same number with the
// release and without it, and a test built on it passes for the wrong reason.
// Resident size across many refreshes is coarse, and it is the claim.
func TestLiveRefreshingTheIconDoesNotGrowTheProcess(t *testing.T) {
	requireWindowServer(t)

	b := &darwinBackend{}
	// WithBackend matters: New() installs a backend of its own, so a tray built
	// without it refreshes a DIFFERENT object than the one attached here and
	// this test measures nothing. It did, at first.
	tr := New(smallPNG(t)).WithBackend(b)
	if err := b.Attach(tr); err != nil {
		t.Fatalf("attach: %v", err)
	}
	icons := [][]byte{smallPNG(t), smallPNG(t)}

	const rounds = 8000
	// Warm up first: the first refreshes fault in AppKit machinery that would
	// otherwise be counted as growth.
	for i := 0; i < 200; i++ {
		objc.AutoreleasePool(func() { tr.SetIcon(icons[i%2]) })
	}
	before := residentKB(t)
	for i := 0; i < rounds; i++ {
		objc.AutoreleasePool(func() { tr.SetIcon(icons[i%2]) })
	}
	after := residentKB(t)

	grew := after - before
	// The allowance is set from measurements on this machine, not guessed:
	// 4000 refreshes grew the process by 31232 KB when the image reference was
	// kept, and by about 5400 KB once it was released and the menu stopped
	// being rebuilt for an icon-only change.
	//
	// The remainder is NOT a leak, and it took a measurement to say so rather
	// than a guess. Growth PER refresh falls as the count rises —
	//
	//      2000 refreshes   4608 KB   2359 bytes each
	//      8000 refreshes   6480 KB    829 bytes each
	//     16000 refreshes   6256 KB    400 bytes each
	//
	// — and the total plateaus around 6 MB whatever the count. That is AppKit
	// warming its caches and reaching a steady state. A leak would have held
	// the per-refresh figure flat.
	const allowKB = 15000
	t.Logf("resident %d KB -> %d KB over %d refreshes (%+d KB)", before, after, rounds, grew)
	if grew > allowKB {
		t.Errorf("the process grew %d KB over %d icon refreshes, allowance %d KB: "+
			"each refresh is stranding an NSImage and its bitmap", grew, rounds, allowKB)
	}
}

// residentKB is this process's resident size, asked of the system rather than
// of the Go runtime, which cannot see an Objective-C allocation at all.
func residentKB(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		t.Skipf("cannot read resident size: %v", err)
	}
	kb, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Skipf("cannot parse resident size %q: %v", out, err)
	}
	return kb
}

// TestLiveRunningTwiceKeepsOneItemInTheMenuBar is the defect a person reported
// by looking at their menu bar: "why does choosing the glasses and confirming
// with Save open a second icon in the tray?".
//
// statusItemWithLength: makes a NEW item every time it is sent, and the item
// already in the bar is retained and does not go away. A program that runs the
// loop, stops it to show a window and takes the loop back afterwards calls
// prepare each time -- go-xrkit/desk does exactly that around its settings
// window -- and grew one more pair of glasses in the menu bar per round trip.
//
// The class and the target instance are checked alongside it for the same
// reason: registering a fresh Objective-C class per call would grow the
// runtime's class table for the life of the process, invisibly.
func TestLiveRunningTwiceKeepsOneItemInTheMenuBar(t *testing.T) {
	requireWindowServer(t)

	b := &darwinBackend{}
	tr := New(nil).WithBackend(b)
	tr.SetMenu(NewMenu().Add(Item("x", func() {})))
	if err := b.Attach(tr); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	item, cls, target := b.item, b.targetCls, b.target
	if item == 0 {
		t.Fatal("no status item after the first attach")
	}

	// Three more times: a person who opens the settings twice must not end up
	// with three icons, and the count has to stay flat rather than merely grow
	// slower.
	for i := range 3 {
		if err := b.Attach(tr); err != nil {
			t.Fatalf("attach %d: %v", i+2, err)
		}
		if b.item != item {
			t.Fatalf("attach %d made a new status item %#x; the first one (%#x) is still "+
				"in the menu bar and nothing can remove it",
				i+2, uintptr(b.item), uintptr(item))
		}
		if b.targetCls != cls {
			t.Errorf("attach %d registered another target class %#x", i+2, uintptr(b.targetCls))
		}
		if b.target != target {
			t.Errorf("attach %d made another target instance %#x", i+2, uintptr(b.target))
		}
	}

	// And the item still WORKS: reused is not the same as left behind. The icon
	// it shows follows the tray, which is what a second run is for.
	tr.SetIcon(pngOf(t, 36, 36, func(x, y int) color.NRGBA {
		return color.NRGBA{A: 0xFF}
	}))
	if got := b.item.Send(objc.Sel("button")).Send(objc.Sel("image")); got == 0 {
		t.Error("the reused item has no image; it is in the bar but no longer follows the tray")
	}
}

// TestLiveMenuItemImageIsSizedForTheRow is the same measurement as
// TestLiveImageIsSizedForTheMenuBar, one layout down.
//
// A row's icon is laid out around the row's TEXT, not around the 22-point bar,
// so it gets its own height. And it fails the same silent way: an NSImage built
// from data reports its pixel count as points, so the 36-pixel PNG the fleet's
// generator emits would arrive in a menu at 36 points -- more than twice the
// label beside it -- with every AppKit call answering normally.
//
// The size is read back OUT of AppKit, off the NSMenuItem the backend actually
// built, rather than off the NSImage the test made: that is the difference
// between "the helper sizes an image" and "the menu on screen carries a sized
// one".
func TestLiveMenuItemImageIsSizedForTheRow(t *testing.T) {
	requireWindowServer(t)

	// 36 pixels, which is what internal/trayicon in godl and the rest of the
	// fleet emits. The number matters: if it equalled menuItemPoints the test
	// would pass without anything being resized.
	if menuItemPoints == 36 {
		t.Fatal("the probe PNG is 36 pixels and menuItemPoints is 36 too: this test would prove nothing")
	}
	glyph := tallPNG(t, 36, 36)

	b := &darwinBackend{}
	tr := New(nil).WithBackend(b)
	tr.SetMenu(NewMenu().Add(
		IconItem("Pause", glyph, func() {}),
		Item("No icon", func() {}),
	))
	if err := b.Attach(tr); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	var (
		withIcon, withoutIcon objc.ID
		size                  objc.NSSize
		isTemplate            bool
		rows                  int
	)
	onMain(t, func() {
		menu := b.item.Send(objc.Sel("menu"))
		if menu == 0 {
			return
		}
		rows = int(menu.Send(objc.Sel("numberOfItems")))
		withIcon = menu.Send(objc.Sel("itemAtIndex:"), 0).Send(objc.Sel("image"))
		withoutIcon = menu.Send(objc.Sel("itemAtIndex:"), 1).Send(objc.Sel("image"))
		if withIcon != 0 {
			size = objc.Send[objc.NSSize](withIcon, objc.Sel("size"))
			isTemplate = withIcon.Send(objc.Sel("isTemplate")) != 0
		}
	})

	t.Logf("rows=%d image=%#x size=%.1fx%.1f template=%v",
		rows, uintptr(withIcon), size.Width, size.Height, isTemplate)

	if withIcon == 0 {
		t.Fatal("the row given an Icon carries no NSImage: setImage: was never sent")
	}
	if size.Height != menuItemPoints {
		t.Errorf("the row's image is %.1f points tall, want %d -- a 36-pixel glyph reached "+
			"the menu at its pixel count", size.Height, menuItemPoints)
	}
	if size.Width != menuItemPoints {
		t.Errorf("a square glyph came back %.1f x %.1f: the aspect ratio was not kept",
			size.Width, size.Height)
	}
	// A monochrome glyph must be a TEMPLATE or it stays black on a dark menu.
	// tallPNG draws one opaque black pixel, so IsTemplate says yes; this reads
	// back what AppKit was actually told.
	if !IsTemplate(glyph) {
		t.Fatal("the probe glyph is not monochrome; the template check below would measure nothing")
	}
	if !isTemplate {
		t.Error("the row's image is not a template: it will stay black on a dark menu")
	}

	// The negative control, in two directions. A row given no icon must have no
	// image -- otherwise "there is an image" says nothing about the field --
	// and an NSImage built from the same bytes without a setSize: must still
	// report its pixels, or the sizing above is guarding a behaviour AppKit no
	// longer has.
	if withoutIcon != 0 {
		t.Errorf("a row with no Icon carries image %#x: every row gets one regardless",
			uintptr(withoutIcon))
	}
	var raw objc.NSSize
	onMain(t, func() {
		unsized := objc.ClassID("NSImage").Send(objc.Sel("alloc")).
			Send(objc.Sel("initWithData:"), objc.ClassID("NSData").
				Send(objc.Sel("dataWithBytes:length:"), unsafe.Pointer(&glyph[0]), uintptr(len(glyph))))
		raw = objc.Send[objc.NSSize](unsized, objc.Sel("size"))
	})
	t.Logf("control: an unsized NSImage of the same bytes reports %.1fx%.1f", raw.Width, raw.Height)
	if raw.Height != 36 {
		t.Fatalf("control: an unsized NSImage reported %.1f points, want 36 -- "+
			"if AppKit no longer does this, the check above is guarding nothing", raw.Height)
	}
}

// TestLiveChangingOnlyAnIconRebuildsTheMenu is the signature defect seen from
// AppKit's side.
//
// menuSignature decides whether the platform menu is rebuilt at all. An icon
// left out of it makes a row whose glyph changes -- play becoming pause, which
// is what this field is for -- keep the old picture for ever: no error, no
// warning, the label updates correctly beside it. The test asserts on the image
// READ BACK from the NSMenuItem, so it fails whether the omission is in the
// signature or in buildMenu.
func TestLiveChangingOnlyAnIconRebuildsTheMenu(t *testing.T) {
	requireWindowServer(t)

	wide := tallPNG(t, 36, 36)  // square
	tallr := tallPNG(t, 18, 36) // half as wide: the sizes differ measurably

	b := &darwinBackend{}
	tr := New(nil).WithBackend(b)
	tr.SetMenu(NewMenu().Add(IconItem("Play", wide, func() {})))
	if err := b.Attach(tr); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	rowImageSize := func() objc.NSSize {
		var sz objc.NSSize
		onMain(t, func() {
			if img := b.item.Send(objc.Sel("menu")).Send(objc.Sel("itemAtIndex:"), 0).
				Send(objc.Sel("image")); img != 0 {
				sz = objc.Send[objc.NSSize](img, objc.Sel("size"))
			}
		})
		return sz
	}

	first := rowImageSize()
	if first.Width != menuItemPoints {
		t.Fatalf("the square glyph came back %.1f wide, want %d", first.Width, menuItemPoints)
	}

	// ONLY the icon changes. Same label, same everything else.
	tr.SetMenu(NewMenu().Add(IconItem("Play", tallr, func() {})))
	second := rowImageSize()
	t.Logf("row image %.1fx%.1f -> %.1fx%.1f", first.Width, first.Height, second.Width, second.Height)

	want := float64(menuItemPoints) / 2
	if second.Width != want {
		t.Errorf("after swapping the glyph the row's image is %.1f wide, want %.1f: "+
			"the menu was not rebuilt, so the old picture is still on screen",
			second.Width, want)
	}
}
