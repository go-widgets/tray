// Copyright (c) 2026 the go-widgets authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package tray

import (
	"bytes"
	"image"
	_ "image/png"
)

// colourfulEnough is how far a pixel's channels must spread before the icon is
// taken to be in colour.
//
// Well above the noise a PNG encoder or an anti-aliased edge produces, and well
// below any colour a person would call one. A grey icon has a spread of zero;
// the green of a status dot has about 150.
const colourfulEnough = 60

// IsTemplate reports whether these PNG bytes should be drawn as a TEMPLATE
// image: a shape the platform recolours to suit its menu bar.
//
// It is decided from the picture rather than asked of the caller, because the
// answer is IN the picture. A monochrome glyph is a template -- macOS draws it
// dark on a light bar, light on a dark one, white while the item is pressed,
// and correct in a tinted bar without anybody choosing a colour. An icon that
// carries colour is not: a template is recoloured, so a green dot meant to say
// "this is running" would come out the same shade as everything around it, and
// the one thing it was for would be gone.
//
// Anything that cannot be decoded is treated as a template, which is what every
// icon was before this existed.
func IsTemplate(iconPNG []byte) bool {
	if len(iconPNG) == 0 {
		return true
	}
	img, _, err := image.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		return true
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			// Out of 16 bits, and compared at 8 so the threshold reads like a
			// colour channel.
			r8, g8, b8 := int(r>>8), int(g>>8), int(bl>>8)
			if spread(r8, g8, b8) >= colourfulEnough {
				return false
			}
		}
	}
	return true
}

// spread is how far apart a pixel's channels are: zero for any grey.
func spread(r, g, b int) int {
	hi, lo := r, r
	if g > hi {
		hi = g
	}
	if b > hi {
		hi = b
	}
	if g < lo {
		lo = g
	}
	if b < lo {
		lo = b
	}
	return hi - lo
}
