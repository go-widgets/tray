// Copyright (c) the go-widgets authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build darwin

package tray

import (
	"errors"
	"testing"

	"github.com/go-macos/objc"
)

// TestLiveMainMenu reads the menu back OUT of NSApp, off the NSMenuItems AppKit
// holds, and then performs a row's action the way a click does.
func TestLiveMainMenu(t *testing.T) {
	requireWindowServer(t)

	// First the refusals a working Mac never takes, through the seams; the class
	// is registered only by the real run below, once per process.
	menus0 := NewMenu()
	boom := errors.New("boom")
	restore := func() {
		mainMenuLoadAppKit, mainMenuApp, mainMenuTarget = ensureAppKit, objc.App, registerMainMenuTarget
		mainMenu = mainMenuState{}
	}
	defer restore()

	mainMenuLoadAppKit = func() error { return boom }
	if err := SetMainMenu("x", menus0); !errors.Is(err, boom) {
		t.Fatalf("a failed AppKit load answered %v, want it", err)
	}
	if err := SetMainMenu("x", menus0); !errors.Is(err, boom) {
		t.Fatalf("the failed prepare was not remembered: %v", err)
	}
	restore()

	mainMenuTarget = func(*mainMenuState, *darwinBackend) (objc.ID, error) { return 0, nil }
	if err := SetMainMenu("x", menus0); !errors.Is(err, ErrNoTargetClass) {
		t.Fatalf("a nil target answered %v, want ErrNoTargetClass", err)
	}
	restore()

	mainMenuApp = func() objc.ID { return 0 }
	if err := SetMainMenu("x", menus0); !errors.Is(err, ErrNoApplication) {
		t.Fatalf("a nil application answered %v, want ErrNoApplication", err)
	}
	// Seams only: this prepare was real, and its class is registered for good.
	mainMenuApp = objc.App

	fired := 0
	menus := NewMenu().Add(
		SubMenu("File", NewMenu().Add(
			KeyItem("Do It", nil, "d", ModCommand, func() { fired++ }),
			Separator(),
			Checkbox("Flag", true, nil),
		)),
		EditMenu(),
	)
	if err := SetMainMenu("Probe", menus); err != nil {
		t.Fatalf("SetMainMenu: %v", err)
	}

	var (
		titles, fileRows, editRows []string
		keys                       []string
		doItIndex                  = -1
		flagState                  int
		appRows                    []string
	)
	onMain(t, func() {
		bar := objc.App().Send(objc.Sel("mainMenu"))
		n := int(bar.Send(objc.Sel("numberOfItems")))
		sub := func(i int) objc.ID { return bar.Send(objc.Sel("itemAtIndex:"), uintptr(i)).Send(objc.Sel("submenu")) }
		rows := func(m objc.ID) (out []string) {
			for j := range int(m.Send(objc.Sel("numberOfItems"))) {
				mi := m.Send(objc.Sel("itemAtIndex:"), uintptr(j))
				if objc.Send[bool](mi, objc.Sel("isSeparatorItem")) {
					out = append(out, "-")
					continue
				}
				out = append(out, objc.Stringify(mi.Send(objc.Sel("title"))))
			}
			return
		}
		for i := range n {
			titles = append(titles, objc.Stringify(sub(i).Send(objc.Sel("title"))))
		}
		if n == 3 {
			appRows, fileRows, editRows = rows(sub(0)), rows(sub(1)), rows(sub(2))
			file := sub(1)
			mi := file.Send(objc.Sel("itemAtIndex:"), uintptr(0))
			keys = append(keys, objc.Stringify(mi.Send(objc.Sel("keyEquivalent"))))
			doItIndex = 0
			flagState = int(file.Send(objc.Sel("itemAtIndex:"), uintptr(2)).Send(objc.Sel("state")))
			// Perform the action as a click does.
			file.Send(objc.Sel("performActionForItemAtIndex:"), uintptr(doItIndex))
			// And the Edit rows carry the responder-chain actions.
			cp := sub(2).Send(objc.Sel("itemAtIndex:"), uintptr(4))
			keys = append(keys, objc.Stringify(cp.Send(objc.Sel("keyEquivalent"))))
			keys = append(keys, objc.Stringify(cp.Send(objc.Sel("title"))))
			if objc.Send[uintptr](cp, objc.Sel("action")) != uintptr(objc.Sel("copy:")) {
				t.Errorf("Edit > Copy does not send -copy:")
			}
			if cp.Send(objc.Sel("target")) != 0 {
				t.Errorf("Edit > Copy has a target; it must go down the responder chain")
			}
		}
	})
	if len(titles) != 3 {
		t.Fatalf("the main menu has %d menus %v, want 3 (app, File, Edit)", len(titles), titles)
	}
	if titles[1] != "File" || titles[2] != "Edit" {
		t.Errorf("titles = %v, want [Probe File Edit]", titles)
	}
	if want := []string{"About Probe", "-", "Hide Probe", "Hide Others", "Show All", "-", "Quit Probe"}; !equal(appRows, want) {
		t.Errorf("app menu = %v, want %v", appRows, want)
	}
	if want := []string{"Do It", "-", "Flag"}; !equal(fileRows, want) {
		t.Errorf("File = %v, want %v", fileRows, want)
	}
	// AppKit appends its own rows (Writing Tools, AutoFill, Dictation, Emoji &
	// Symbols) to a menu titled Edit, so the standard rows are a prefix.
	if want := []string{"Undo", "Redo", "-", "Cut", "Copy", "Paste", "Select All"}; len(editRows) < len(want) || !equal(editRows[:len(want)], want) {
		t.Errorf("Edit = %v, want %v", editRows, want)
	}
	if keys[0] != "d" || keys[1] != "c" {
		t.Errorf("key equivalents = %v, want d and c", keys)
	}
	if flagState != 1 {
		t.Errorf("checked row state = %d, want 1", flagState)
	}
	if fired != 1 {
		t.Errorf("performing the row fired its action %d times, want 1", fired)
	}

	// Change-only: the same menu again must not rebuild (same NSMenu object).
	var before, after objc.ID
	onMain(t, func() { before = objc.App().Send(objc.Sel("mainMenu")) })
	if err := SetMainMenu("Probe", menus); err != nil {
		t.Fatal(err)
	}
	onMain(t, func() { after = objc.App().Send(objc.Sel("mainMenu")) })
	if before != after {
		t.Error("an unchanged menu was installed again")
	}
	menus.Items[0].Submenu.Items[0].Label = "Do That"
	if err := SetMainMenu("Probe", menus); err != nil {
		t.Fatal(err)
	}
	onMain(t, func() { after = objc.App().Send(objc.Sel("mainMenu")) })
	if before == after {
		t.Error("a changed label was not applied")
	}

	// The class name is taken now, so a second registration is refused.
	if _, err := registerMainMenuTarget(&mainMenuState{}, &darwinBackend{}); !errors.Is(err, ErrNoTargetClass) {
		t.Errorf("a second registration answered %v, want ErrNoTargetClass", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
