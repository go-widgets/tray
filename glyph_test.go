package tray

import (
	"image"
	"image/color"
	"testing"
)

// paddedGlyphPNG is a canvas x canvas PNG that is transparent except for an
// opaque black mark filling ink: the shape that was reported, a glyph centred in
// padding.
func paddedGlyphPNG(canvas int, ink image.Rectangle) []byte {
	m := image.NewNRGBA(image.Rect(0, 0, canvas, canvas))
	for y := ink.Min.Y; y < ink.Max.Y; y++ {
		for x := ink.Min.X; x < ink.Max.X; x++ {
			m.SetNRGBA(x, y, color.NRGBA{A: 255})
		}
	}
	return encodePNG(m)
}

func mustDecode(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	img, err := decodeNRGBA(b)
	if err != nil || img == nil {
		t.Fatalf("decode: %v %v", img, err)
	}
	return img
}

func TestInkBoundsIgnoresPaddingAndFringe(t *testing.T) {
	img := mustDecode(t, paddedGlyphPNG(18, image.Rect(3, 4, 15, 15)))
	if got, want := inkBounds(img), image.Rect(3, 4, 15, 15); got != want {
		t.Fatalf("ink = %v, want %v", got, want)
	}
	// A near-transparent stray pixel is not part of the mark.
	img.SetNRGBA(0, 0, color.NRGBA{A: inkAlpha - 1})
	if got := inkBounds(img); got != image.Rect(3, 4, 15, 15) {
		t.Fatalf("a faint pixel moved the ink to %v", got)
	}
	// Nothing to crop to: the whole image comes back.
	empty := image.NewNRGBA(image.Rect(0, 0, 5, 7))
	if got := inkBounds(empty); got != empty.Bounds() {
		t.Fatalf("empty ink = %v, want the bounds", got)
	}
}

func TestDecodeNRGBA(t *testing.T) {
	if img, err := decodeNRGBA(nil); !img.Bounds().Empty() || err != nil {
		t.Fatalf("no icon: %v %v", img.Bounds(), err)
	}
	if img, err := decodeNRGBA([]byte("not a png")); err == nil || !img.Bounds().Empty() {
		t.Fatal("garbage must be an error and an empty image")
	}
	// An empty source fits to one clear pixel instead of dividing by zero.
	if g := fitInk(&image.NRGBA{}, 16); g.Bounds() != image.Rect(0, 0, 1, 1) || g.Pix[3] != 0 {
		t.Fatalf("empty source fits to %v", g.Bounds())
	}
}

func TestFitInkMakesTheInkTheSize(t *testing.T) {
	wide := mustDecode(t, paddedGlyphPNG(18, image.Rect(3, 4, 15, 15))) // 12 x 11
	if g := fitInk(wide, 16); g.Bounds().Dx() != 16 || g.Bounds().Dy() != 15 {
		t.Fatalf("12x11 ink at 16 is %v, want 16x15", g.Bounds())
	}
	tall := mustDecode(t, paddedGlyphPNG(20, image.Rect(8, 2, 12, 18))) // 4 x 16
	if g := fitInk(tall, 16); g.Bounds().Dx() != 4 || g.Bounds().Dy() != 16 {
		t.Fatalf("4x16 ink at 16 is %v, want 4x16", g.Bounds())
	}
	sliver := mustDecode(t, paddedGlyphPNG(100, image.Rect(0, 50, 100, 51))) // 100 x 1
	if g := fitInk(sliver, 16); g.Bounds().Dx() != 16 || g.Bounds().Dy() != 1 {
		t.Fatalf("a sliver collapsed to %v, want 16x1", g.Bounds())
	}
	thin := mustDecode(t, paddedGlyphPNG(100, image.Rect(50, 0, 51, 100))) // 1 x 100
	if g := fitInk(thin, 16); g.Bounds().Dx() != 1 || g.Bounds().Dy() != 16 {
		t.Fatalf("a thin column collapsed to %v, want 1x16", g.Bounds())
	}
	if g := fitInk(wide, 0); g.Bounds().Dx() != 1 {
		t.Fatalf("a zero size must still be one pixel, got %v", g.Bounds())
	}
}

// Scaling must not bleed the transparent surround into the mark: an opaque red
// block stays opaque red, edges included, and what was clear stays clear.
func TestResampleKeepsColourAndAlpha(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 2; y < 6; y++ {
		for x := 2; x < 6; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 200, A: 255})
		}
	}
	g := resample(src, image.Rect(0, 0, 8, 8), 4, 4)
	if c := g.NRGBAAt(1, 1); c != (color.NRGBA{R: 200, A: 255}) {
		t.Errorf("inside the block: %v", c)
	}
	if c := g.NRGBAAt(0, 0); c.A != 0 {
		t.Errorf("the clear corner became %v", c)
	}
	// Up, as for the 2x bitmap of a small source.
	up := resample(src, image.Rect(2, 2, 6, 6), 8, 8)
	for _, p := range [][2]int{{0, 0}, {7, 7}, {3, 4}} {
		if c := up.NRGBAAt(p[0], p[1]); c != (color.NRGBA{R: 200, A: 255}) {
			t.Errorf("upscaled block at %v: %v", p, c)
		}
	}
	// Half-covered edge: alpha is the coverage, colour is untouched.
	half := resample(src, image.Rect(1, 2, 3, 3), 1, 1) // one clear, one opaque pixel
	if half.NRGBAAt(0, 0).R != 200 || half.NRGBAAt(0, 0).A != 128 {
		t.Errorf("half-covered pixel is %v, want R200 A128", half.NRGBAAt(0, 0))
	}
}

func TestGlyphPNGsAre1xAnd2xOfOnePointSize(t *testing.T) {
	w, h, one, two, err := glyphPNGs(paddedGlyphPNG(18, image.Rect(3, 4, 15, 15)))
	if err != nil {
		t.Fatal(err)
	}
	if w != menuBarGlyphPoints || h != 15 {
		t.Fatalf("point size %dx%d, want 16x15", w, h)
	}
	a, b := mustDecode(t, one).Bounds(), mustDecode(t, two).Bounds()
	if a.Dx() != w || a.Dy() != h {
		t.Errorf("1x is %v pixels, want %dx%d", a, w, h)
	}
	if b.Dx() != 2*menuBarGlyphPoints || b.Dy() != 30 {
		t.Errorf("2x is %v pixels, want 32x30", b)
	}
	if w, h, one, two, err := glyphPNGs(nil); w != 0 || h != 0 || one != nil || two != nil || err != nil {
		t.Errorf("no icon: %d %d %v %v %v", w, h, one, two, err)
	}
	if _, _, _, _, err := glyphPNGs([]byte("x")); err == nil {
		t.Error("garbage must be an error")
	}
}

// An animated icon asks for the same frames over and over: the second ask is the
// first one's answer, not a new decode, and the memory held stays bounded.
func TestGlyphPNGsAreRememberedAndBounded(t *testing.T) {
	src := paddedGlyphPNG(18, image.Rect(3, 4, 15, 15))
	_, _, one, _, _ := glyphPNGs(src)
	_, _, again, _, _ := glyphPNGs(src)
	if len(one) == 0 || &one[0] != &again[0] {
		t.Fatal("the same source was encoded twice")
	}
	if _, _, _, _, err := glyphPNGs([]byte("x")); err == nil {
		t.Fatal("garbage must be an error")
	}
	if _, _, _, _, err := glyphPNGs([]byte("x")); err == nil {
		t.Error("a remembered failure must stay a failure")
	}
	for i := 0; i < 3*glyphCacheMax; i++ {
		glyphPNGs(paddedGlyphPNG(18+i, image.Rect(3, 4, 15, 15)))
	}
	glyphMu.Lock()
	n := len(glyphMemo)
	glyphMu.Unlock()
	if n > glyphCacheMax {
		t.Errorf("%d sources remembered, bound is %d", n, glyphCacheMax)
	}
}

// The glyph fills the same share of its cell whatever padding its source carried,
// which is what makes it the size of its neighbours.
func TestGlyphSizeDoesNotDependOnThePadding(t *testing.T) {
	tight := paddedGlyphPNG(12, image.Rect(0, 0, 12, 12))
	loose := paddedGlyphPNG(48, image.Rect(18, 18, 30, 30))
	for _, n := range sniSizes {
		a := inkBounds(squareIcon(mustDecode(t, tight), n, squareFill))
		b := inkBounds(squareIcon(mustDecode(t, loose), n, squareFill))
		if a != b {
			t.Errorf("size %d: ink %v for the tight source, %v for the padded one", n, a, b)
		}
		if got := a.Dy(); got < int(float64(n)*squareFill)-1 || got > int(float64(n)*squareFill)+1 {
			t.Errorf("size %d: glyph is %d pixels, want about %.0f", n, got, float64(n)*squareFill)
		}
		if d := (a.Min.X - (n - a.Max.X)); d < -1 || d > 1 {
			t.Errorf("size %d: glyph %v is not centred", n, a)
		}
	}
}

func TestSNIIcons(t *testing.T) {
	icons := sniIcons(paddedGlyphPNG(18, image.Rect(3, 4, 15, 15)))
	if len(icons) != len(sniSizes) {
		t.Fatalf("%d pixmaps, want %d", len(icons), len(sniSizes))
	}
	for i, ic := range icons {
		if ic.Size != sniSizes[i] || len(ic.ARGB) != 4*ic.Size*ic.Size {
			t.Errorf("pixmap %d: size %d with %d bytes", i, ic.Size, len(ic.ARGB))
		}
		// ARGB, network order: the centre pixel is opaque black, alpha first.
		c := 4 * (ic.Size/2*ic.Size + ic.Size/2)
		if ic.ARGB[c] != 255 || ic.ARGB[c+1] != 0 {
			t.Errorf("pixmap %d: centre pixel % x is not opaque black in ARGB", i, ic.ARGB[c:c+4])
		}
	}
	if sniIcons(nil) != nil || sniIcons([]byte("x")) != nil {
		t.Error("no usable icon must yield no pixmaps")
	}
}

// The sizes a watcher is offered include the cells real panels use.
func TestSNISizesCoverRealCells(t *testing.T) {
	have := map[int]bool{}
	for _, n := range sniSizes {
		have[n] = true
	}
	for _, want := range []int{16, 22, 24, 32, 48} {
		if !have[want] {
			t.Errorf("no %d px pixmap", want)
		}
	}
}
