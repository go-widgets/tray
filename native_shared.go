package tray

// Pure, platform-independent helpers shared by the native backends: PNG icon
// decoding, pixel-format packing and menu flattening. Keeping them here (no
// build tags) lets the core test suite cover the tricky bit-twiddling and the
// menu-walking logic while the OS-specific event loops stay compile-only.

import (
	"bytes"
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
