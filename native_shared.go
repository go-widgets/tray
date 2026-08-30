package tray

// Pure, platform-independent helpers shared by the native backends: PNG icon
// decoding, pixel-format packing and menu flattening. Keeping them here (no
// build tags) lets the core test suite cover the tricky bit-twiddling and the
// menu-walking logic while the OS-specific event loops stay compile-only.

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/png"
)

// pngPixels decodes PNG-encoded data into a tightly packed, top-down RGBA pixel
// buffer (4 bytes per pixel in R,G,B,A order, straight/non-premultiplied alpha)
// together with the image width and height. Empty input yields a nil buffer and
// zero dimensions with no error, so a tray with no icon degrades gracefully.
func pngPixels(data []byte) (pix []byte, w, h int, err error) {
	if len(data) == 0 {
		return nil, 0, 0, nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	nr := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(nr, nr.Bounds(), img, b.Min, draw.Src)
	return nr.Pix, w, h, nil
}

// toARGB repacks a top-down RGBA pixel buffer into the ARGB32 network-byte-order
// layout (A,R,G,B per pixel) that a StatusNotifierItem IconPixmap expects.
func toARGB(rgba []byte) []byte {
	out := make([]byte, len(rgba))
	for i := 0; i+3 < len(rgba); i += 4 {
		r, g, b, a := rgba[i], rgba[i+1], rgba[i+2], rgba[i+3]
		out[i+0] = a
		out[i+1] = r
		out[i+2] = g
		out[i+3] = b
	}
	return out
}

// toBGRA repacks a top-down RGBA pixel buffer into the B,G,R,A byte order used by
// a Win32 32-bpp bitmap (a little-endian 0xAARRGGBB pixel).
func toBGRA(rgba []byte) []byte {
	out := make([]byte, len(rgba))
	for i := 0; i+3 < len(rgba); i += 4 {
		r, g, b, a := rgba[i], rgba[i+1], rgba[i+2], rgba[i+3]
		out[i+0] = b
		out[i+1] = g
		out[i+2] = r
		out[i+3] = a
	}
	return out
}

// leafItems returns every actionable item (neither a separator nor a submenu
// parent) in m in depth-first, pre-order sequence. A backend that addresses
// items by an integer command id (eg. Win32 TrackPopupMenu) maps that id back to
// a *MenuItem through the returned slice. A nil menu yields no items.
func leafItems(m *Menu) []*MenuItem {
	var out []*MenuItem
	var walk func(*Menu)
	walk = func(mm *Menu) {
		for _, it := range mm.Items {
			switch {
			case it.Separator:
				// separators are not actionable
			case it.Submenu != nil:
				walk(it.Submenu)
			default:
				out = append(out, it)
			}
		}
	}
	if m != nil {
		walk(m)
	}
	return out
}

// dispatchLeaf activates items[tag], ignoring an out-of-range tag. A backend
// that addresses menu items by a flat integer tag (eg. an NSMenuItem tag)
// resolves a click through it: the tag indexes straight into the leafItems
// slice the menu was built from, and a stale or malformed tag is a safe no-op
// rather than an out-of-bounds panic.
func dispatchLeaf(items []*MenuItem, tag int) {
	if tag >= 0 && tag < len(items) {
		items[tag].Activate()
	}
}

// walkItems returns every item in m — separators and submenu parents included —
// in depth-first, parent-before-children order. Its index in the returned slice
// is a stable id a backend can address an item by (eg. a dbusmenu node id). A
// nil menu yields no items.
func walkItems(m *Menu) []*MenuItem {
	var out []*MenuItem
	var walk func(*Menu)
	walk = func(mm *Menu) {
		for _, it := range mm.Items {
			out = append(out, it)
			if it.Submenu != nil {
				walk(it.Submenu)
			}
		}
	}
	if m != nil {
		walk(m)
	}
	return out
}

// ---------------------------------------------------------------------------
// The nil-object guard shared by the native macOS backend.
// ---------------------------------------------------------------------------

// Errors reported by the native macOS backend when AppKit hands back nothing.
// They are stable and may be tested with errors.Is.
var (
	// ErrNoApplication reports that +[NSApplication sharedApplication] yielded
	// nil, which on a Mac with a window server means one thing: AppKit is not
	// loaded in this process, so the NSApplication class does not exist and the
	// class lookup returned the nil class.
	ErrNoApplication = errors.New("tray: +[NSApplication sharedApplication] returned nil (AppKit is not loaded in this process)")

	// ErrNoStatusBar reports that +[NSStatusBar systemStatusBar] yielded nil.
	// There is no menu bar to put anything in: no window server, or a session
	// that has none.
	ErrNoStatusBar = errors.New("tray: +[NSStatusBar systemStatusBar] returned nil (no menu bar in this session)")

	// ErrNoStatusItem reports that -[NSStatusBar statusItemWithLength:] yielded
	// nil: the menu bar exists but refused this process an item in it.
	ErrNoStatusItem = errors.New("tray: -[NSStatusBar statusItemWithLength:] returned nil (the menu bar refused this process an item)")

	// ErrNoTargetClass reports that the Objective-C class carrying the menu
	// action could not be created. Every clickable row needs an instance of it
	// as its target, and a row whose target is nil draws perfectly and answers
	// no click.
	ErrNoTargetClass = errors.New("tray: the Objective-C action target class could not be created")
)

// checkNative reports why the macOS backend cannot show a status item, given
// the four Objective-C objects it needs, or nil when all four are live.
//
// WHY THIS FUNCTION EXISTS. It is the whole lesson of the defect it was written
// for. Objective-C answers a message to nil with zero and says nothing, so one
// nil object propagates silently through every call after it: a nil
// NSApplication gives a nil status bar, a nil item and a nil button, and then
// [NSApp run] returns immediately. What that looks like from outside is a
// menu-bar program that starts, prints its greeting, exits in half a second,
// puts nothing on screen and writes nothing to a log — a symptom that points at
// nothing in particular and cost seven diagnostics. The runtime will not
// complain, so the code has to complain in its place.
//
// The ORDER is deliberate, not incidental. app is reported first because every
// later nil is its consequence, and telling a caller "no menu bar in this
// session" about a process that simply never loaded AppKit sends them to look
// at their display arrangement for a defect that is in their imports.
//
// It takes uintptr rather than an Objective-C object type, and lives outside
// the darwin build tag, so every branch of it is reachable from the ordinary
// test lane on every platform — the branches that matter here are exactly the
// ones a machine with a working menu bar can never take.
func checkNative(app, statusBar, item, target uintptr) error {
	switch {
	case app == 0:
		return ErrNoApplication
	case statusBar == 0:
		return ErrNoStatusBar
	case item == 0:
		return ErrNoStatusItem
	case target == 0:
		return ErrNoTargetClass
	}
	return nil
}
