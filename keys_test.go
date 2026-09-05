// Copyright (c) the go-widgets authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package tray

import "testing"

// TestKeyItemCarriesEverythingARowNeeds.
//
// The three things a row is made of arrive together, because a row that says
// what it does, shows a glyph for it and names the key that does the same thing
// is one thought and not three.
func TestKeyItemCarriesEverythingARowNeeds(t *testing.T) {
	glyph := []byte("not really a PNG, and this constructor does not decode one")
	fired := 0
	it := KeyItem("Settings...", glyph, "s", ModControl|ModOption|ModCommand, func() { fired++ })

	if it.Label != "Settings..." {
		t.Errorf("Label = %q", it.Label)
	}
	if string(it.Icon) != string(glyph) {
		t.Errorf("the icon was not carried through")
	}
	if it.Key != "s" {
		t.Errorf("Key = %q", it.Key)
	}
	if it.Mods != ModControl|ModOption|ModCommand {
		t.Errorf("Mods = %b", it.Mods)
	}
	if it.Separator || it.Disabled || it.Submenu != nil {
		t.Error("a plain row came back as something else")
	}
	it.Activate()
	if fired != 1 {
		t.Errorf("the row fired %d times", fired)
	}
}

// TestTheModifiersAreDistinctBits, so a set of them is a set: two flags sharing
// a bit would draw one glyph for two keys and nobody would see why.
func TestTheModifiersAreDistinctBits(t *testing.T) {
	all := []Mods{ModCommand, ModShift, ModOption, ModControl}
	var seen Mods
	for _, m := range all {
		if m == 0 {
			t.Error("a modifier is zero, which is the absence of one")
		}
		if m&(m-1) != 0 {
			t.Errorf("%b is more than one bit", m)
		}
		if seen&m != 0 {
			t.Errorf("%b shares a bit with one already counted", m)
		}
		seen |= m
	}
}

// TestTheGlyphKeysAreTheValuesAppKitExpects.
//
// ⛔ Not a decoration. These constants go straight into
// -[NSMenuItem setKeyEquivalent:], which reads the arrows as the Unicode
// private-use codes the function keys have had since NeXT, and Return, Escape
// and Delete as their control characters. A "left arrow" spelled any other way
// draws nothing at all.
func TestTheGlyphKeysAreTheValuesAppKitExpects(t *testing.T) {
	for _, c := range []struct {
		got  string
		want rune
		name string
	}{
		{KeyUp, 0xF700, "NSUpArrowFunctionKey"},
		{KeyDown, 0xF701, "NSDownArrowFunctionKey"},
		{KeyLeft, 0xF702, "NSLeftArrowFunctionKey"},
		{KeyRight, 0xF703, "NSRightArrowFunctionKey"},
		{KeyReturn, '\r', "carriage return"},
		{KeyEscape, 0x1B, "escape"},
		{KeyDelete, '\b', "backspace"},
		{KeyForwardDelete, 0x7F, "forward delete"},
		{KeyTab, '\t', "tab"},
		{KeySpace, ' ', "space"},
	} {
		r := []rune(c.got)
		if len(r) != 1 || r[0] != c.want {
			t.Errorf("%s is %q, want %#U", c.name, c.got, c.want)
		}
	}
}

// TestTheFunctionKeysAreDistinctAndInOrder.
//
// ⛔ A WRONG CODE HERE IS SILENT. A key equivalent AppKit does not recognise
// draws nothing -- exactly what an unbound row draws -- so a transposed digit
// would show up as "the shortcut is missing from the menu again" and nothing
// else. The block is consecutive from NSF1FunctionKey, which is checkable.
func TestTheFunctionKeysAreDistinctAndInOrder(t *testing.T) {
	fkeys := []string{
		KeyF1, KeyF2, KeyF3, KeyF4, KeyF5, KeyF6, KeyF7, KeyF8,
		KeyF9, KeyF10, KeyF11, KeyF12, KeyF13, KeyF14, KeyF15,
	}
	const nsF1 = 0xF704
	for i, k := range fkeys {
		r := []rune(k)
		if len(r) != 1 {
			t.Fatalf("F%d is %q, which is not one character", i+1, k)
		}
		if want := rune(nsF1 + i); r[0] != want {
			t.Errorf("F%d is %#04x, want %#04x", i+1, r[0], want)
		}
	}
	// And none of them collides with a key that already had a meaning: the
	// arrows live in the same private-use block, four codes below F1.
	seen := map[string]string{
		KeyUp: "Up", KeyDown: "Down", KeyLeft: "Left", KeyRight: "Right",
		KeyReturn: "Return", KeyEscape: "Escape", KeyDelete: "Delete",
		KeyForwardDelete: "ForwardDelete", KeyTab: "Tab", KeySpace: "Space",
	}
	for i, k := range fkeys {
		if was, ok := seen[k]; ok {
			t.Errorf("F%d has the same code as %s", i+1, was)
		}
		seen[k] = "F" + string(rune('0'+i))
	}
}
