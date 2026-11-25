// Copyright (c) the go-widgets authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package tray

import (
	"errors"
	"fmt"
	"runtime"
)

// Role marks a [MenuItem] as one of the standard commands a platform already
// knows how to carry out. A row with a Role is not routed to its OnClick on
// macOS: it is sent down the responder chain (-copy:, -paste:, -hide:,
// -terminate: ...), so Copy copies out of whichever native text field has the
// focus and Quit quits, with no Go code between the click and the effect.
//
// ⛔ That is the whole reason roles exist. A native field copies and pastes only
// when the application's main menu has a row bound to -copy: and -paste: with
// the usual key equivalents; an application with a main menu that lacks them
// cannot copy out of its own fields at all, and nothing says why.
//
// A row with a Role and an OnClick calls the OnClick on platforms that have no
// responder chain, so an application can still give those a behaviour.
type Role uint8

const (
	// RoleNone is a plain row, activated through OnClick.
	RoleNone Role = iota
	RoleAbout
	RoleHide
	RoleHideOthers
	RoleShowAll
	RoleQuit
	RoleUndo
	RoleRedo
	RoleCut
	RoleCopy
	RolePaste
	RoleSelectAll
	RoleMinimize
	RoleZoom
)

// roleSpec is what a Role stands for: the AppKit action it sends, a label and
// key equivalent used when the row gives none of its own.
type roleSpec struct {
	selector string
	label    string
	key      string
	mods     Mods
}

var roleSpecs = map[Role]roleSpec{
	RoleAbout:      {"orderFrontStandardAboutPanel:", "About", "", 0},
	RoleHide:       {"hide:", "Hide", "h", ModCommand},
	RoleHideOthers: {"hideOtherApplications:", "Hide Others", "h", ModCommand | ModOption},
	RoleShowAll:    {"unhideAllApplications:", "Show All", "", 0},
	RoleQuit:       {"terminate:", "Quit", "q", ModCommand},
	RoleUndo:       {"undo:", "Undo", "z", ModCommand},
	RoleRedo:       {"redo:", "Redo", "z", ModCommand | ModShift},
	RoleCut:        {"cut:", "Cut", "x", ModCommand},
	RoleCopy:       {"copy:", "Copy", "c", ModCommand},
	RolePaste:      {"paste:", "Paste", "v", ModCommand},
	RoleSelectAll:  {"selectAll:", "Select All", "a", ModCommand},
	RoleMinimize:   {"performMiniaturize:", "Minimize", "m", ModCommand},
	RoleZoom:       {"performZoom:", "Zoom", "", 0},
}

// RoleItem is a row for a standard command, with that command's usual label and
// key equivalent.
func RoleItem(r Role) *MenuItem {
	s := roleSpecs[r]
	return &MenuItem{Label: s.label, Key: s.key, Mods: s.mods, Role: r}
}

// EditMenu is the standard Edit menu: Undo, Redo, Cut, Copy, Paste and Select
// All, bound to the responder chain. It is the menu an application needs for
// copy and paste to work in a native text field; put it among the menus given to
// [SetMainMenu] (or application.Config.Menu).
func EditMenu() *MenuItem {
	return SubMenu("Edit", NewMenu().Add(
		RoleItem(RoleUndo), RoleItem(RoleRedo), Separator(),
		RoleItem(RoleCut), RoleItem(RoleCopy), RoleItem(RolePaste),
		RoleItem(RoleSelectAll),
	))
}

// ErrMainMenuUnsupported is what [SetMainMenu] answers on a platform whose main
// menu belongs to a window rather than to the process (Windows, GTK): use the
// window package's SetMenuBar there. errors.Is finds it.
var ErrMainMenuUnsupported = errors.New("tray: the main menu of this platform belongs to a window; use window.SetMenuBar")

// SetMainMenu installs menus as the application's main menu bar, and is safe to
// call from any goroutine and as often as the menus change.
//
// menus is a [Menu] whose rows are the TOP-LEVEL menus: each row's Label is the
// menu's title and its Submenu the menu itself. The application's own menu
// (About, Hide, Hide Others, Show All, Quit, under appName) is placed first for
// you, as macOS requires, so menus holds only what comes after it.
//
// Nothing happens when menus is the same as what is installed, by
// [Menu.Signature]: re-applying an unchanged menu would replace it under a user
// who has it open. A different handler is picked up without a rebuild, because
// the click table is always refreshed.
//
// Implemented on macOS (NSApp's mainMenu). Elsewhere it answers
// [ErrMainMenuUnsupported].
func SetMainMenu(appName string, menus *Menu) error {
	return setMainMenu(appName, menus)
}

func unsupportedMainMenu() error {
	return fmt.Errorf("%w (%s)", ErrMainMenuUnsupported, runtime.GOOS)
}
