// Copyright (c) the go-widgets authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build darwin

package tray

import (
	"testing"

	"github.com/go-macos/objc"
)

// TestLiveMenuItemCarriesItsKeyEquivalent.
//
// ⛔ Read back OUT of AppKit, off the NSMenuItem the backend actually built,
// and not off the MenuItem the test handed in. That is the difference between
// "the struct has a field" and "the menu on screen draws the combination" --
// and drawing it is the whole point: a caller who only wanted to say which key
// does this could have put it in the label, and would then have had it
// left-aligned in the middle of the row.
//
// The modifier mask is the part that cannot be guessed. NSEventModifierFlags
// has a gap in it -- shift at bit 17, control 18, option 19, command 20 -- so a
// cast of the package's own flags would draw the wrong glyphs beside the key,
// which is a menu telling somebody to press something that does nothing.
func TestLiveMenuItemCarriesItsKeyEquivalent(t *testing.T) {
	requireWindowServer(t)

	b := &darwinBackend{}
	tr := New(nil).WithBackend(b)
	tr.SetMenu(NewMenu().Add(
		KeyItem("Settings...", nil, "s", ModControl|ModOption|ModCommand, func() {}),
		KeyItem("Previous", nil, KeyLeft, ModCommand, func() {}),
		Item("No key at all", func() {}),
	))
	if err := b.Attach(tr); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Otherwise it would leave a real item in the menu bar for the rest of this
	// binary's life.
	t.Cleanup(func() { b.Remove(tr) })

	var (
		rows          int
		keys          []string
		masks         []uint64
		labels        []string
		gotWindowless bool
	)
	onMain(t, func() {
		menu := b.item.Send(objc.Sel("menu"))
		if menu == 0 {
			gotWindowless = true
			return
		}
		rows = int(menu.Send(objc.Sel("numberOfItems")))
		for i := range rows {
			mi := menu.Send(objc.Sel("itemAtIndex:"), uintptr(i))
			keys = append(keys, objc.Stringify(mi.Send(objc.Sel("keyEquivalent"))))
			masks = append(masks, uint64(mi.Send(objc.Sel("keyEquivalentModifierMask"))))
			labels = append(labels, objc.Stringify(mi.Send(objc.Sel("title"))))
		}
	})
	if gotWindowless {
		t.Skip("no menu on the status item in this session")
	}
	if rows != 3 {
		t.Fatalf("the menu has %d rows, want 3", rows)
	}

	const (
		nsShift   = 1 << 17
		nsControl = 1 << 18
		nsOption  = 1 << 19
		nsCommand = 1 << 20
	)
	if keys[0] != "s" {
		t.Errorf("the first row's key equivalent is %q, want %q", keys[0], "s")
	}
	if want := uint64(nsControl | nsOption | nsCommand); masks[0] != want {
		t.Errorf("the first row's modifier mask is %#x, want %#x -- AppKit draws "+
			"the glyphs from this, so the wrong bits name the wrong keys",
			masks[0], want)
	}
	if masks[0]&nsShift != 0 {
		t.Error("shift is set on a combination that did not ask for it")
	}
	if keys[1] != KeyLeft {
		t.Errorf("the arrow row's key equivalent is %q, want the left-arrow "+
			"function key: a menu draws an arrow there, not the word", keys[1])
	}
	if want := uint64(nsCommand); masks[1] != want {
		t.Errorf("the arrow row's modifier mask is %#x, want %#x", masks[1], want)
	}

	// A row given no key has no key equivalent, and draws nothing.
	//
	// ⚠ ITS MASK IS NOT ZERO, and that is AppKit's doing rather than ours: a
	// fresh NSMenuItem carries NSEventModifierFlagCommand whether or not it has
	// a character to wear it, so the mask on its own says nothing at all. It is
	// the CHARACTER that decides whether there is a key equivalent -- measured
	// here, because a test asserting a zero mask would fail on a row nobody had
	// touched.
	if keys[2] != "" {
		t.Errorf("a row given no key has the equivalent %q", keys[2])
	}
	if masks[2] != nsCommand {
		t.Logf("a fresh NSMenuItem's default mask is %#x, not the %#x this was "+
			"written against; harmless, but the comment above is now wrong",
			masks[2], nsCommand)
	}

	// And the label is still the label: the combination is drawn beside it by
	// the platform, not appended to it by us.
	if labels[0] != "Settings..." {
		t.Errorf("the first row's title is %q", labels[0])
	}
}

// TestModifierMaskIsATranslationAndNotACast, on every combination, without a
// window server: the table is portable arithmetic and the gap in
// NSEventModifierFlags is what it exists for.
func TestModifierMaskIsATranslationAndNotACast(t *testing.T) {
	const (
		nsShift   = 1 << 17
		nsControl = 1 << 18
		nsOption  = 1 << 19
		nsCommand = 1 << 20
	)
	for _, c := range []struct {
		mods Mods
		want uintptr
	}{
		{0, 0},
		{ModCommand, nsCommand},
		{ModShift, nsShift},
		{ModOption, nsOption},
		{ModControl, nsControl},
		{ModControl | ModOption | ModCommand, nsControl | nsOption | nsCommand},
		{ModCommand | ModShift | ModOption | ModControl, nsCommand | nsShift | nsOption | nsControl},
	} {
		if got := nsModifierMask(c.mods); got != c.want {
			t.Errorf("nsModifierMask(%b) = %#x, want %#x", c.mods, got, c.want)
		}
	}
	// A cast would pass every test above only by accident; this is the one that
	// says it is a translation.
	if uintptr(ModCommand) == nsModifierMask(ModCommand) {
		t.Error("the package's flag and AppKit's mask are the same number: " +
			"one of the two tables has been changed to match the other")
	}
}
