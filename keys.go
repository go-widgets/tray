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

	// The function keys, which a menu draws as "F3" rather than as a glyph.
	//
	// ⛔ THEY HAVE NO CHARACTER OF THEIR OWN, and a row bound to one draws
	// NOTHING without these -- silently, because an empty key equivalent is
	// also what a row nothing was granted for has. Found when two menu rows
	// moved onto F3 and F4 and their shortcuts vanished from the menu, with
	// no error anywhere: the reader is simply told less than before.
	//
	// The codes are AppKit's own, NSF1FunctionKey upwards, in the same
	// private-use block as the arrows above.
	KeyF1  = ""
	KeyF2  = ""
	KeyF3  = ""
	KeyF4  = ""
	KeyF5  = ""
	KeyF6  = ""
	KeyF7  = ""
	KeyF8  = ""
	KeyF9  = ""
	KeyF10 = ""
	KeyF11 = ""
	KeyF12 = ""
	KeyF13 = ""
	KeyF14 = ""
	KeyF15 = ""
)

// KeyItem is a clickable menu item with an icon and a key equivalent.
//
// The long form of [IconItem]: a row that says what it does, shows a glyph for
// it, and names the key that does the same thing.
func KeyItem(label string, iconPNG []byte, key string, mods Mods, onClick func()) *MenuItem {
	return &MenuItem{Label: label, Icon: iconPNG, Key: key, Mods: mods, OnClick: onClick}
}
