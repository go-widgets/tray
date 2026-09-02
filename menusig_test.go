// Copyright (c) 2026, the go-widgets authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package tray

import "testing"

// The signature answers one question: would this menu DRAW differently? Each
// case below is a way of getting that wrong that would either rebuild a menu
// for nothing or, worse, leave a stale one on screen.
func TestMenuSignatureSeesWhatIsDrawn(t *testing.T) {
	base := func() *Menu {
		return NewMenu().Add(
			&MenuItem{Label: "Downloading", Disabled: true},
			Separator(),
			Item("Pause", func() {}),
			&MenuItem{Label: "More", Submenu: NewMenu().Add(Item("Quit", nil))},
		)
	}

	if menuSignature(base()) != menuSignature(base()) {
		t.Fatal("two identical menus have different signatures: every refresh would rebuild")
	}
	if menuSignature(nil) != menuSignature(nil) {
		t.Error("nil is not stable")
	}
	if menuSignature(nil) == menuSignature(NewMenu()) {
		t.Error("no menu and an empty menu are not the same thing")
	}

	for _, tc := range []struct {
		name   string
		change func(*Menu)
		differ bool
	}{
		{"a label", func(m *Menu) { m.Items[0].Label = "Paused" }, true},
		{"a tooltip", func(m *Menu) { m.Items[0].Tooltip = "why" }, true},
		{"disabled", func(m *Menu) { m.Items[2].Disabled = true }, true},
		{"checked", func(m *Menu) { m.Items[2].Checked = true }, true},
		{"a separator becoming an item", func(m *Menu) { m.Items[1].Separator = false }, true},
		{"an item inside a submenu", func(m *Menu) { m.Items[3].Submenu.Items[0].Label = "Leave" }, true},
		{"a submenu disappearing", func(m *Menu) { m.Items[3].Submenu = nil }, true},
		{"one more item", func(m *Menu) { m.Add(Item("Extra", nil)) }, true},

		// The icon is drawn, so changing it must rebuild. Without this a media
		// row going from play to pause -- the case MenuItem.Icon was added for
		// -- keeps the old glyph for ever while its label updates around it,
		// and nothing anywhere reports a failure.
		{"an icon appearing", func(m *Menu) { m.Items[2].Icon = []byte("play") }, true},
		{"an icon inside a submenu", func(m *Menu) { m.Items[3].Submenu.Items[0].Icon = []byte("x") }, true},

		// The handler is not drawn. Comparing function values is impossible
		// anyway, and the click table is rebuilt on every refresh regardless.
		{"the click handler", func(m *Menu) { m.Items[2].OnClick = func() { panic("x") } }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := base()
			b := base()
			tc.change(b)
			if differ := menuSignature(a) != menuSignature(b); differ != tc.differ {
				t.Errorf("changing %s: signatures differ = %v, want %v", tc.name, differ, tc.differ)
			}
		})
	}
}

// The icon is hashed by CONTENT, not by slice identity: a caller that rebuilds
// its menu from scratch every tick hands over a fresh []byte holding the same
// PNG each time, and calling that a change would rebuild the platform menu
// several times a second -- replacing it under a user who has it open, which is
// the cost this signature exists to avoid.
func TestMenuSignatureComparesIconContentNotIdentity(t *testing.T) {
	glyph := func() []byte { return []byte{0x89, 'P', 'N', 'G', 1, 2, 3} }
	a := NewMenu().Add(IconItem("Pause", glyph(), nil))
	b := NewMenu().Add(IconItem("Pause", glyph(), nil))
	if menuSignature(a) != menuSignature(b) {
		t.Error("two equal icons signed differently: every refresh would rebuild the menu")
	}

	c := NewMenu().Add(IconItem("Pause", []byte{0x89, 'P', 'N', 'G', 1, 2, 4}, nil))
	if menuSignature(a) == menuSignature(c) {
		t.Error("two different icons signed the same: the changed glyph would never be drawn")
	}

	// And an icon is not the same as no icon, which is the transition a row
	// makes the first time it is given one.
	if menuSignature(a) == menuSignature(NewMenu().Add(Item("Pause", nil))) {
		t.Error("a row with an icon signed the same as the same row without one")
	}
}

// Lengths go with the icon bytes too. Without the length these two menus --
// which draw different glyphs on different rows -- would concatenate to the
// same bytes and be called equal.
func TestMenuSignatureIsNotFooledByRunTogetherIcons(t *testing.T) {
	a := NewMenu().Add(IconItem("x", []byte{1, 2}, nil), IconItem("y", []byte{3}, nil))
	b := NewMenu().Add(IconItem("x", []byte{1}, nil), IconItem("y", []byte{2, 3}, nil))
	if menuSignature(a) == menuSignature(b) {
		t.Error("icons [1,2]+[3] and [1]+[2,3] hash the same: a changed menu would never be redrawn")
	}
}

// Lengths go into the hash with the strings. Without them these two menus —
// which draw completely differently — would be called equal, and the second
// would never reach the screen.
func TestMenuSignatureIsNotFooledByRunTogetherLabels(t *testing.T) {
	a := NewMenu().Add(Item("ab", nil), Item("c", nil))
	b := NewMenu().Add(Item("a", nil), Item("bc", nil))
	if menuSignature(a) == menuSignature(b) {
		t.Error(`"ab"+"c" and "a"+"bc" hash the same: a changed menu would never be redrawn`)
	}
}

func TestMenuSignatureSurvivesANilItem(t *testing.T) {
	m := NewMenu()
	m.Items = append(m.Items, nil)
	if menuSignature(m) == "" {
		t.Error("a nil item should be signed, not crash")
	}
	if menuSignature(m) == menuSignature(NewMenu()) {
		t.Error("a menu with a nil item is not an empty menu")
	}
}
