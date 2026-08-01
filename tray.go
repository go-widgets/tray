// Package tray is a cross-platform system-tray (menu-bar) widget for go-widgets.
//
// A tray icon is OS-integration, not a pixel-blitted widget, so it cannot live
// in the pure-blitting toolkit. This package models the tray, its menu and menu
// items in a platform-agnostic core, and drives them through a small Backend
// interface implemented per-OS:
//
//   - darwin:  NSStatusItem + NSMenu via purego + the Objective-C runtime
//   - windows: Shell_NotifyIcon + TrackPopupMenu via x/sys/windows
//   - linux:   StatusNotifierItem + com.canonical.dbusmenu over DBus
//
// All CGO_ENABLED=0. A headless backend backs tests and display-less CI.
package tray

import "errors"

// ErrNoBackend is returned by Run when no platform backend has been set (and
// none was selected for the current OS).
var ErrNoBackend = errors.New("tray: no backend for this platform")

// MenuItem is one entry in a tray menu.
type MenuItem struct {
	Label     string
	Tooltip   string
	Checked   bool
	Disabled  bool
	Separator bool
	// OnClick is invoked when the item is activated. For a checkbox item the
	// Checked field is toggled before OnClick runs.
	OnClick func()
	// Submenu, when non-nil, makes this item open a nested menu (its OnClick is
	// then ignored).
	Submenu *Menu

	checkbox bool
	onToggle func(bool)
}

// Menu is an ordered list of items.
type Menu struct {
	Items []*MenuItem
}

// NewMenu returns an empty menu.
func NewMenu() *Menu { return &Menu{} }

// Add appends items and returns the menu for chaining.
func (m *Menu) Add(items ...*MenuItem) *Menu {
	m.Items = append(m.Items, items...)
	return m
}

// Item is a plain clickable menu item.
func Item(label string, onClick func()) *MenuItem {
	return &MenuItem{Label: label, OnClick: onClick}
}

// Separator is a divider line.
func Separator() *MenuItem { return &MenuItem{Separator: true} }

// Checkbox is a toggleable item; onToggle receives the new checked state.
func Checkbox(label string, checked bool, onToggle func(bool)) *MenuItem {
	return &MenuItem{Label: label, Checked: checked, checkbox: true, onToggle: onToggle}
}

// SubMenu is an item that opens a nested menu.
func SubMenu(label string, sub *Menu) *MenuItem {
	return &MenuItem{Label: label, Submenu: sub}
}

// Activate dispatches a click on the item: it flips a checkbox's state and
// invokes the appropriate callback. Separators, disabled items and submenu
// parents do nothing. Backends call this when the user picks an item.
func (it *MenuItem) Activate() {
	if it.Separator || it.Disabled || it.Submenu != nil {
		return
	}
	if it.checkbox {
		it.Checked = !it.Checked
		if it.onToggle != nil {
			it.onToggle(it.Checked)
		}
		return
	}
	if it.OnClick != nil {
		it.OnClick()
	}
}

// Find returns the item at the given path of indices (descending into
// submenus), or nil if the path is invalid.
func (m *Menu) Find(path ...int) *MenuItem {
	cur := m
	var item *MenuItem
	for _, i := range path {
		if cur == nil || i < 0 || i >= len(cur.Items) {
			return nil
		}
		item = cur.Items[i]
		cur = item.Submenu
	}
	return item
}

// Backend drives a Tray on a specific platform.
type Backend interface {
	// Run shows the tray and blocks on the platform event loop until Quit.
	Run(t *Tray) error
	// Refresh re-applies the tray's icon, tooltip and menu after a change.
	Refresh(t *Tray)
	// Quit stops the event loop started by Run.
	Quit()
}

// Tray is a system-tray icon with a tooltip and a menu.
type Tray struct {
	icon    []byte // PNG bytes
	tooltip string
	menu    *Menu
	backend Backend
	onReady func()
}

// New creates a tray showing iconPNG (PNG-encoded bytes). The platform backend
// is selected automatically; use WithBackend to override (eg. for tests).
func New(iconPNG []byte) *Tray {
	return &Tray{icon: iconPNG, menu: NewMenu(), backend: defaultBackend()}
}

// WithBackend overrides the platform backend and returns the tray.
func (t *Tray) WithBackend(b Backend) *Tray { t.backend = b; return t }

// SetTooltip sets the hover tooltip and refreshes if running.
func (t *Tray) SetTooltip(s string) *Tray {
	t.tooltip = s
	t.refresh()
	return t
}

// SetIcon replaces the icon (PNG bytes) and refreshes if running.
func (t *Tray) SetIcon(iconPNG []byte) *Tray {
	t.icon = iconPNG
	t.refresh()
	return t
}

// SetMenu sets the tray menu and refreshes if running.
func (t *Tray) SetMenu(m *Menu) *Tray {
	t.menu = m
	t.refresh()
	return t
}

// OnReady registers a callback run once the tray is live (after Run starts).
func (t *Tray) OnReady(fn func()) *Tray { t.onReady = fn; return t }

// Accessors used by backends.
func (t *Tray) Icon() []byte    { return t.icon }
func (t *Tray) Tooltip() string { return t.tooltip }
func (t *Tray) Menu() *Menu     { return t.menu }

// ready is called by a backend once its event loop is live.
func (t *Tray) ready() {
	if t.onReady != nil {
		t.onReady()
	}
}

// Run shows the tray and blocks until Quit. It errors if no backend is set.
func (t *Tray) Run() error {
	if t.backend == nil {
		return ErrNoBackend
	}
	return t.backend.Run(t)
}

// Quit stops the tray's event loop.
func (t *Tray) Quit() {
	if t.backend != nil {
		t.backend.Quit()
	}
}

func (t *Tray) refresh() {
	if t.backend != nil {
		t.backend.Refresh(t)
	}
}
