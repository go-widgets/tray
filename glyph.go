package tray

// Pure, platform-independent sizing of a tray glyph. The native backends hand
// the shell a picture it will draw at a size of ITS choosing, so the picture has
// to arrive already the right size and already free of the padding a designer
// left around the mark: a 12-pixel glyph centred in an 18-pixel canvas is a
// glyph two thirds the height of its neighbours, however correctly the canvas is
// sized. Everything here is a function of pixels and the answers are tested on
// every platform; only the last step (hand the bitmap to AppKit, Win32 or the
// StatusNotifierWatcher) is platform code.

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"sync"
)

// menuBarGlyphPoints is the length, in points, of the longer side of the INK of
// a macOS menu-bar glyph. The bar is about 22 points high; Apple's own extras
// (Wi-Fi, Control Center, the clock's icon) draw a glyph of about 16 points with
// no padding baked into the image. The canvas is the ink: see [glyphPNGs].
const menuBarGlyphPoints = 16

// inkAlpha is the alpha, out of 255, from which a pixel counts as part of the
// mark rather than as anti-aliasing fringe or an encoder's stray near-zero.
const inkAlpha = 32

// inkBounds is the smallest rectangle holding every pixel of img whose alpha is
// at least inkAlpha. An image with no such pixel yields its whole bounds, so an
// all-transparent (or unreadable-as-ink) picture is left alone instead of being
// cropped to nothing.
func inkBounds(img *image.NRGBA) image.Rectangle {
	b := img.Bounds()
	out := image.Rectangle{Min: b.Max, Max: b.Min}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A < inkAlpha {
				continue
			}
			if x < out.Min.X {
				out.Min.X = x
			}
			if y < out.Min.Y {
				out.Min.Y = y
			}
			if x+1 > out.Max.X {
				out.Max.X = x + 1
			}
			if y+1 > out.Max.Y {
				out.Max.Y = y + 1
			}
		}
	}
	if out.Empty() {
		return b
	}
	return out
}

// decodeNRGBA decodes PNG data into a straight-alpha image at the origin. It
// never returns nil: no data, or data that does not decode, is an EMPTY image
// (callers test Bounds().Empty()) so a missing guard downstream produces a wrong
// answer a test can assert on rather than a nil dereference.
func decodeNRGBA(data []byte) (*image.NRGBA, error) {
	pix, w, h, err := pngPixels(data)
	if err != nil {
		return &image.NRGBA{}, err
	}
	return &image.NRGBA{Pix: pix, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}, nil
}

// resample scales the part r of src to w x h pixels by area averaging in
// premultiplied alpha, so a glyph keeps its weight when it shrinks and a
// transparent edge does not bleed a dark halo into it. Scaling up uses the same
// rule (each destination pixel integrates the source area it covers), which is a
// soft, never ringing enlargement.
func resample(src *image.NRGBA, r image.Rectangle, w, h int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	sx := float64(r.Dx()) / float64(w)
	sy := float64(r.Dy()) / float64(h)
	for dy := 0; dy < h; dy++ {
		y0, y1 := float64(dy)*sy, float64(dy+1)*sy
		for dx := 0; dx < w; dx++ {
			x0, x1 := float64(dx)*sx, float64(dx+1)*sx
			var pr, pg, pb, pa, wsum float64
			for yy := int(math.Floor(y0)); float64(yy) < y1; yy++ {
				wy := math.Min(y1, float64(yy+1)) - math.Max(y0, float64(yy))
				for xx := int(math.Floor(x0)); float64(xx) < x1; xx++ {
					wt := wy * (math.Min(x1, float64(xx+1)) - math.Max(x0, float64(xx)))
					c := src.NRGBAAt(r.Min.X+xx, r.Min.Y+yy)
					a := float64(c.A) / 255
					pr += wt * a * float64(c.R)
					pg += wt * a * float64(c.G)
					pb += wt * a * float64(c.B)
					pa += wt * a
					wsum += wt
				}
			}
			if pa <= 0 {
				continue
			}
			i := dst.PixOffset(dx, dy)
			dst.Pix[i+0] = uint8(math.Round(pr / pa))
			dst.Pix[i+1] = uint8(math.Round(pg / pa))
			dst.Pix[i+2] = uint8(math.Round(pb / pa))
			dst.Pix[i+3] = uint8(math.Round(255 * pa / wsum))
		}
	}
	return dst
}

// fitInk crops src to its ink and scales it so the longer side is longest
// pixels (never less than 1) and the other side keeps the aspect ratio.
func fitInk(src *image.NRGBA, longest int) *image.NRGBA {
	ink := inkBounds(src)
	w, h := ink.Dx(), ink.Dy()
	if ink.Empty() { // an empty source: one clear pixel, not a division by zero
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	if longest < 1 {
		longest = 1
	}
	tw, th := longest, longest
	if w >= h {
		th = int(math.Round(float64(longest) * float64(h) / float64(w)))
	} else {
		tw = int(math.Round(float64(longest) * float64(w) / float64(h)))
	}
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}
	return resample(src, ink, tw, th)
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	// png.Encode only fails when the writer does, and a bytes.Buffer never does.
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// glyphPNGs turns the tray's icon into the 1x and 2x bitmaps of one image that
// is wPts x hPts POINTS: the 1x is that many pixels, the 2x twice as many. The
// ink is cropped out of whatever padding the source carries and its longer side
// is menuBarGlyphPoints, so the glyph is the size of the system's own. Empty
// input yields no bitmaps and no error (a tray without an icon).
//
// The answer is remembered per source, because an animated icon asks for the
// same few frames again and again and the decode, crop, two resamples and two
// encodes are Go heap garbage each time (measured on a CI runner: +17 to +28 MB
// resident over 8000 refreshes of two 18-pixel frames before this). The memory
// is bounded by glyphCacheMax sources; the slices are shared and read-only.
func glyphPNGs(src []byte) (wPts, hPts int, one, two []byte, err error) {
	glyphMu.Lock()
	e, ok := glyphMemo[string(src)]
	glyphMu.Unlock()
	if ok {
		return e.w, e.h, e.one, e.two, e.err
	}
	wPts, hPts, one, two, err = buildGlyphPNGs(src)
	glyphMu.Lock()
	if len(glyphMemo) >= glyphCacheMax {
		clear(glyphMemo)
	}
	glyphMemo[string(src)] = glyphEntry{wPts, hPts, one, two, err}
	glyphMu.Unlock()
	return wPts, hPts, one, two, err
}

// glyphCacheMax bounds [glyphPNGs]'s memory: a tray shows a handful of icons.
const glyphCacheMax = 32

type glyphEntry struct {
	w, h     int
	one, two []byte
	err      error
}

var (
	glyphMu   sync.Mutex
	glyphMemo = map[string]glyphEntry{}
)

func buildGlyphPNGs(src []byte) (wPts, hPts int, one, two []byte, err error) {
	img, err := decodeNRGBA(src)
	if err != nil || img.Bounds().Empty() {
		return 0, 0, nil, nil, err
	}
	g1 := fitInk(img, menuBarGlyphPoints)
	wPts, hPts = g1.Bounds().Dx(), g1.Bounds().Dy()
	// The 2x is exactly twice the 1x in both directions, not fitted on its own:
	// rounding the two separately gave 15 and 29, representations that do not
	// describe the same size in points.
	g2 := resample(img, inkBounds(img), 2*wPts, 2*hPts)
	return wPts, hPts, encodePNG(g1), encodePNG(g2), nil
}

// squareIcon is the icon as an n x n picture: the ink fitted into fill (0..1) of
// the side and centred, the rest transparent. Shells that take one square size
// (a Win32 small icon, an SNI pixmap) draw the whole canvas, so the margin that
// belongs around a glyph is made here, in proportion, rather than inherited from
// the source's arbitrary padding.
func squareIcon(src *image.NRGBA, n int, fill float64) *image.NRGBA {
	g := fitInk(src, int(math.Round(float64(n)*fill)))
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	ox, oy := (n-g.Bounds().Dx())/2, (n-g.Bounds().Dy())/2
	for y := 0; y < g.Bounds().Dy(); y++ {
		copy(dst.Pix[dst.PixOffset(ox, oy+y):], g.Pix[g.PixOffset(0, y):g.PixOffset(0, y)+4*g.Bounds().Dx()])
	}
	return dst
}

// squareFill is how much of a square tray icon the glyph occupies: the notification
// area draws a 16-pixel glyph in a 22-pixel cell, and Windows' own icons leave a
// pixel or two around a 16-pixel face.
const squareFill = 0.8

// sniSizes are the IconPixmap edge lengths a StatusNotifierItem offers. The
// watcher picks the one nearest its cell (22 on KDE and most panels, 16 to 48
// elsewhere, doubled on a HiDPI one), so giving several is what keeps the icon
// the host's size instead of whatever single size the source happened to be.
var sniSizes = []int{16, 22, 24, 32, 48, 64}

// sniIcon is one square IconPixmap: its edge in pixels and its ARGB32 bytes.
type sniIcon struct {
	Size int
	ARGB []byte
}

// sniIcons is the icon at every size in sniSizes, glyph fitted and centred. An
// empty or undecodable icon yields none.
func sniIcons(src []byte) []sniIcon {
	img, err := decodeNRGBA(src)
	if err != nil || img.Bounds().Empty() {
		return nil
	}
	out := make([]sniIcon, 0, len(sniSizes))
	for _, n := range sniSizes {
		out = append(out, sniIcon{Size: n, ARGB: toARGB(squareIcon(img, n, squareFill).Pix)})
	}
	return out
}
