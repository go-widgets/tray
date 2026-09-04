// Copyright (c) the go-widgets authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package tray

// Mods are the modifier keys shown beside a row's [MenuItem.Key].
//
// The package's own flags rather than the platform's: AppKit's mask, a Windows
// accelerator table and a DBus menu's shortcut string agree on nothing but the
// four keys themselves, so the portable half names those and each backend
// translates.
type Mods uint

const (
	// ModCommand is ⌘. First because it is the modifier a menu row almost
	// always has, so a set with one bit in it is usually this one.
	ModCommand Mods = 1 << iota
	// ModShift is ⇧.
	ModShift
	// ModOption is ⌥, which a PC keyboard prints as Alt.
	ModOption
	// ModControl is ⌃.
	ModControl
)

// The keys a menu draws as a GLYPH rather than as a character.
//
// They are the values AppKit expects in a key equivalent: the Unicode
// private-use codes for the function keys, and the control characters for the
// three that have one. A caller writes [KeyLeft] and gets ←, drawn by the
// platform in the platform's own arrow.
//
// There is no constant for a letter or a digit: "s" and "7" are the key
// equivalent, and inventing a name for each would be a table to keep in step
// with a keyboard.
const (
	KeyUp     = "\uF700"
	KeyDown   = "\uF701"
	KeyLeft   = "\uF702"
	KeyRight  = "\uF703"
	KeyReturn = "\r"
	KeyEscape = "\x1b"
	// KeyDelete is the BACKSPACE key -- the one above Return, which macOS
	// prints as ⌫. The forward delete key is [KeyForwardDelete].
	KeyDelete        = "\b"
	KeyForwardDelete = "\x7f"
	KeyTab           = "\t"
	KeySpace         = " "
)

// KeyItem is a clickable menu item with an icon and a key equivalent.
//
// The long form of [IconItem]: a row that says what it does, shows a glyph for
// it, and names the key that does the same thing.
func KeyItem(label string, iconPNG []byte, key string, mods Mods, onClick func()) *MenuItem {
	return &MenuItem{Label: label, Icon: iconPNG, Key: key, Mods: mods, OnClick: onClick}
}
