// Copyright (c) 2026, the go-widgets authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package tray

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

// menuSignature is everything about a menu that a platform menu can show: the
// structure, and the fields that end up on screen. Two menus with the same
// signature draw the same thing.
//
// It deliberately ignores OnClick. A handler that changes does not change what
// is drawn, and comparing function values is not possible anyway — the click
// table is rebuilt from leafItems() on the same walk, so the right handler is
// found whether or not the platform menu was touched.
//
// It exists so a backend can tell "the icon changed" from "the menu changed".
// Once an icon animates, a backend that rebuilds its menu on every refresh
// rebuilds it several times a second: it allocates for nothing, and it replaces
// the menu under a user who has it open.
func menuSignature(m *Menu) string {
	h := sha256.New()
	writeMenu(h, m)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func writeMenu(h io.Writer, m *Menu) {
	if m == nil {
		_, _ = h.Write([]byte{0})
		return
	}
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(m.Items)))
	_, _ = h.Write(n[:])
	for _, it := range m.Items {
		if it == nil {
			_, _ = h.Write([]byte{1})
			continue
		}
		// Lengths go in with the strings: without them "ab"+"c" and "a"+"bc"
		// hash the same, and two different menus would be called equal.
		writeStr(h, it.Label)
		writeStr(h, it.Tooltip)
		// The icon is DRAWN, so it belongs here. A menu whose only change is a
		// row's icon -- a media row going from play to pause, which is the
		// whole reason the field exists -- would otherwise sign the same as
		// the menu already on screen and never be rebuilt: the label would
		// update and the glyph would not. That is exactly the silent class
		// this file exists to prevent, so it is hashed by CONTENT: two
		// different slices holding the same PNG are the same picture and must
		// not cost a rebuild.
		writeBytes(h, it.Icon)
		_, _ = h.Write([]byte{
			2,
			b2b(it.Checked), b2b(it.Disabled), b2b(it.Separator),
			b2b(it.Submenu != nil),
		})
		if it.Submenu != nil {
			writeMenu(h, it.Submenu)
		}
	}
}

func writeStr(h io.Writer, s string) {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(s))
}

// writeBytes hashes a byte field, length first, for the same reason writeStr
// does: without the length a two-byte icon followed by a one-byte one signs the
// same as a one followed by a two.
func writeBytes(h io.Writer, b []byte) {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(b)
}

func b2b(v bool) byte {
	if v {
		return 1
	}
	return 0
}
