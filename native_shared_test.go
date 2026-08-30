package tray

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// smallPNG encodes a 2x1 NRGBA image with two known pixels.
func smallPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44})
	img.Set(1, 0, color.NRGBA{R: 0xaa, G: 0xbb, B: 0xcc, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPNGPixels(t *testing.T) {
	// empty input degrades to a nil buffer, no error
	if pix, w, h, err := pngPixels(nil); pix != nil || w != 0 || h != 0 || err != nil {
		t.Errorf("empty: %v %d %d %v", pix, w, h, err)
	}
	// invalid PNG returns an error
	if _, _, _, err := pngPixels([]byte("not a png")); err == nil {
		t.Error("expected decode error for garbage input")
	}
	// valid PNG round-trips to straight-alpha RGBA
	pix, w, h, err := pngPixels(smallPNG(t))
	if err != nil || w != 2 || h != 1 {
		t.Fatalf("decode = %v %d %d", err, w, h)
	}
	want := []byte{0x11, 0x22, 0x33, 0x44, 0xaa, 0xbb, 0xcc, 0xff}
	if !bytes.Equal(pix, want) {
		t.Errorf("pixels = % x, want % x", pix, want)
	}
}

func TestToARGB(t *testing.T) {
	if got := toARGB(nil); len(got) != 0 {
		t.Errorf("empty argb = % x", got)
	}
	// R,G,B,A -> A,R,G,B
	got := toARGB([]byte{0x11, 0x22, 0x33, 0x44})
	want := []byte{0x44, 0x11, 0x22, 0x33}
	if !bytes.Equal(got, want) {
		t.Errorf("argb = % x, want % x", got, want)
	}
}

func TestToBGRA(t *testing.T) {
	if got := toBGRA(nil); len(got) != 0 {
		t.Errorf("empty bgra = % x", got)
	}
	// R,G,B,A -> B,G,R,A
	got := toBGRA([]byte{0x11, 0x22, 0x33, 0x44})
	want := []byte{0x33, 0x22, 0x11, 0x44}
	if !bytes.Equal(got, want) {
		t.Errorf("bgra = % x, want % x", got, want)
	}
}

func TestLeafItems(t *testing.T) {
	if got := leafItems(nil); got != nil {
		t.Errorf("nil menu leaves = %v", got)
	}
	leafA := Item("A", nil)
	leafSub := Item("sub", nil)
	leafB := Item("B", nil)
	m := NewMenu().Add(
		leafA,
		Separator(),
		SubMenu("more", NewMenu().Add(leafSub)),
		leafB,
	)
	got := leafItems(m)
	want := []*MenuItem{leafA, leafSub, leafB}
	if len(got) != len(want) {
		t.Fatalf("leaves = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("leaf[%d] = %q, want %q", i, got[i].Label, want[i].Label)
		}
	}
}

func TestDispatchLeaf(t *testing.T) {
	a, b := 0, 0
	items := []*MenuItem{Item("A", func() { a++ }), Item("B", func() { b++ })}
	// in-range tags activate the mapped item
	dispatchLeaf(items, 0)
	dispatchLeaf(items, 1)
	if a != 1 || b != 1 {
		t.Fatalf("in-range dispatch = %d %d, want 1 1", a, b)
	}
	// out-of-range and negative tags are safe no-ops
	dispatchLeaf(items, 2)
	dispatchLeaf(items, -1)
	dispatchLeaf(nil, 0)
	if a != 1 || b != 1 {
		t.Fatalf("out-of-range dispatch mutated state: %d %d", a, b)
	}
}

func TestWalkItems(t *testing.T) {
	if got := walkItems(nil); got != nil {
		t.Errorf("nil menu walk = %v", got)
	}
	a := Item("A", nil)
	sep := Separator()
	sub := SubMenu("more", NewMenu().Add(Item("child", nil)))
	m := NewMenu().Add(a, sep, sub)
	got := walkItems(m)
	// pre-order, every node included: A, sep, sub, child
	if len(got) != 4 {
		t.Fatalf("walk = %d, want 4", len(got))
	}
	if got[0] != a || got[1] != sep || got[2] != sub {
		t.Error("walk order wrong at top level")
	}
	if got[3].Label != "child" {
		t.Errorf("walk[3] = %q, want child", got[3].Label)
	}
}

// TestCheckNativeNamesTheObjectThatIsNil is the regression test for the defect
// that made this guard necessary: a tray that started, said it was in the menu
// bar and exited half a second later, because +[NSApplication sharedApplication]
// had returned nil in a process where nothing had loaded AppKit and every
// message after it returned zero in silence.
//
// Each case leaves exactly one object nil, and the expectation is the error that
// NAMES that object. The order of the cases is itself the assertion the last one
// makes: with several objects nil at once the first cause must be reported, not
// the last symptom.
func TestCheckNativeNamesTheObjectThatIsNil(t *testing.T) {
	const live = 0x1 // any non-nil pointer value; only zero-ness is read

	for _, c := range []struct {
		name                         string
		app, statusBar, item, target uintptr
		want                         error
	}{
		{"all live", live, live, live, live, nil},
		{"no application", 0, live, live, live, ErrNoApplication},
		{"no status bar", live, 0, live, live, ErrNoStatusBar},
		{"no status item", live, live, 0, live, ErrNoStatusItem},
		{"no target class", live, live, live, 0, ErrNoTargetClass},
		// The whole cascade, which is what the real failure looked like: AppKit
		// was never loaded, so nothing downstream of it existed either. The
		// application must be reported, because the other three nils are its
		// consequence and naming them sends the reader to the wrong place.
		{"nothing at all", 0, 0, 0, 0, ErrNoApplication},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkNative(c.app, c.statusBar, c.item, c.target)
			if !errors.Is(got, c.want) {
				t.Fatalf("checkNative(%#x,%#x,%#x,%#x) = %v, want %v",
					c.app, c.statusBar, c.item, c.target, got, c.want)
			}
		})
	}
}

// TestNativeErrorsAreDistinct guards the thing the table above cannot: four
// errors that all said the same thing would satisfy every case there while
// telling an operator nothing.
func TestNativeErrorsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, err := range []error{ErrNoApplication, ErrNoStatusBar, ErrNoStatusItem, ErrNoTargetClass} {
		if err.Error() == "" {
			t.Error("an error with an empty message")
		}
		if seen[err.Error()] {
			t.Errorf("two errors share the message %q", err)
		}
		seen[err.Error()] = true
	}
}
