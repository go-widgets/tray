// Copyright (c) the go-widgets authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package tray

import (
	"errors"
	"runtime"
	"testing"
)

func TestEditMenuHasTheStandardRows(t *testing.T) {
	e := EditMenu()
	if e.Label != "Edit" || e.Submenu == nil {
		t.Fatalf("EditMenu = %+v, want a submenu titled Edit", e)
	}
	want := []struct {
		label string
		role  Role
		key   string
		mods  Mods
	}{
		{"Undo", RoleUndo, "z", ModCommand},
		{"Redo", RoleRedo, "z", ModCommand | ModShift},
		{"", RoleNone, "", 0}, // separator
		{"Cut", RoleCut, "x", ModCommand},
		{"Copy", RoleCopy, "c", ModCommand},
		{"Paste", RolePaste, "v", ModCommand},
		{"Select All", RoleSelectAll, "a", ModCommand},
	}
	if len(e.Submenu.Items) != len(want) {
		t.Fatalf("Edit has %d rows, want %d", len(e.Submenu.Items), len(want))
	}
	for i, w := range want {
		it := e.Submenu.Items[i]
		if w.label == "" {
			if !it.Separator {
				t.Errorf("row %d is not a separator", i)
			}
			continue
		}
		if it.Label != w.label || it.Role != w.role || it.Key != w.key || it.Mods != w.mods {
			t.Errorf("row %d = {%q %v %q %v}, want {%q %v %q %v}", i,
				it.Label, it.Role, it.Key, it.Mods, w.label, w.role, w.key, w.mods)
		}
	}
}

func TestEveryRoleHasASpec(t *testing.T) {
	for r := RoleAbout; r <= RoleZoom; r++ {
		s, ok := roleSpecs[r]
		if !ok || s.selector == "" || s.label == "" {
			t.Errorf("role %d has no usable spec: %+v", r, s)
		}
	}
	if _, ok := roleSpecs[RoleNone]; ok {
		t.Error("RoleNone must not have a spec: it is a plain row")
	}
}

// A shortcut is drawn, so a change to it is a change: re-applying must happen.
func TestSignatureSeesAKeyEquivalentAndARole(t *testing.T) {
	base := func() *Menu { return NewMenu().Add(KeyItem("Go", nil, "g", ModCommand, nil)) }
	sig := base().Signature()
	if sig != base().Signature() {
		t.Fatal("two identical menus sign differently")
	}
	k := base()
	k.Items[0].Key = "h"
	m := base()
	m.Items[0].Mods = ModCommand | ModShift
	r := base()
	r.Items[0].Role = RoleCopy
	for name, mm := range map[string]*Menu{"key": k, "mods": m, "role": r} {
		if mm.Signature() == sig {
			t.Errorf("a changed %s signs the same as before", name)
		}
	}
	var nilMenu *Menu
	if nilMenu.Signature() == sig {
		t.Error("a nil menu signs like a real one")
	}
}

func TestRoleItemFallsBackToOnClickWhereThereIsNoResponderChain(t *testing.T) {
	clicked := 0
	it := RoleItem(RoleQuit)
	it.OnClick = func() { clicked++ }
	it.Activate()
	if clicked != 1 {
		t.Fatalf("OnClick ran %d times, want 1", clicked)
	}
}

func TestMainMenuSetupErrors(t *testing.T) {
	boom := errors.New("boom")
	if err := mainMenuTargetError(5, boom); !errors.Is(err, boom) {
		t.Errorf("a registration error was lost: %v", err)
	}
	if err := mainMenuTargetError(0, nil); !errors.Is(err, ErrNoTargetClass) {
		t.Errorf("a nil target answered %v", err)
	}
	if err := mainMenuTargetError(5, nil); err != nil {
		t.Errorf("a live target answered %v", err)
	}
	if err := mainMenuAppError(0); !errors.Is(err, ErrNoApplication) {
		t.Errorf("a nil application answered %v", err)
	}
	if err := mainMenuAppError(1); err != nil {
		t.Errorf("a live application answered %v", err)
	}
}

func TestSetMainMenuIsRefusedOffMacOS(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("implemented on macOS; see the live test")
	}
	err := SetMainMenu("x", NewMenu())
	if !errors.Is(err, ErrMainMenuUnsupported) {
		t.Fatalf("SetMainMenu = %v, want ErrMainMenuUnsupported", err)
	}
}
