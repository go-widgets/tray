// Copyright (c) 2026 the go-widgets authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// pngOf encodes one small picture, drawn by fill.
func pngOf(t *testing.T, w, h int, fill func(x, y int) color.NRGBA) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, fill(x, y))
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return b.Bytes()
}

func TestAMonochromeIconIsATemplateAndAColouredOneIsNot(t *testing.T) {
	black := func(int, int) color.NRGBA { return color.NRGBA{A: 255} }
	grey := func(x, y int) color.NRGBA { return color.NRGBA{R: 120, G: 120, B: 120, A: 255} }
	// A glyph with one green pixel: a status dot is small, and being small must
	// not stop it counting.
	dotted := func(x, y int) color.NRGBA {
		if x == 9 && y == 9 {
			return color.NRGBA{R: 40, G: 200, B: 80, A: 255}
		}
		return color.NRGBA{A: 255}
	}
	// Transparent everywhere but a green pixel: the alpha-zero pixels must not
	// be read as grey and drown it.
	mostlyEmpty := func(x, y int) color.NRGBA {
		if x == 1 && y == 1 {
			return color.NRGBA{R: 40, G: 200, B: 80, A: 255}
		}
		return color.NRGBA{}
	}

	for _, c := range []struct {
		name string
		png  []byte
		want bool
	}{
		{"a black glyph", pngOf(t, 10, 10, black), true},
		{"a grey glyph", pngOf(t, 10, 10, grey), true},
		{"a glyph with a green dot", pngOf(t, 10, 10, dotted), false},
		{"one green pixel on nothing", pngOf(t, 4, 4, mostlyEmpty), false},
		{"no picture at all", nil, true},
		{"something that is not a picture", []byte("this is not a PNG"), true},
	} {
		if got := IsTemplate(c.png); got != c.want {
			t.Errorf("%s: IsTemplate = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSpreadIsZeroForEveryGrey(t *testing.T) {
	for _, v := range []int{0, 1, 128, 254, 255} {
		if got := spread(v, v, v); got != 0 {
			t.Errorf("spread(%d,%d,%d) = %d, want 0", v, v, v, got)
		}
	}
	// And it is the distance between the extremes, whichever channel they are.
	for _, c := range [][4]int{
		{255, 0, 0, 255},
		{0, 255, 0, 255},
		{0, 0, 255, 255},
		{10, 40, 20, 30},
		{40, 10, 20, 30}, // the low one first
		{20, 10, 40, 30}, // and the high one last
		{200, 5, 100, 195},
		{50, 40, 10, 40}, // blue lowest of the three
	} {
		if got := spread(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("spread(%d,%d,%d) = %d, want %d", c[0], c[1], c[2], got, c[3])
		}
	}
}
