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

import (
	"errors"
	"sync"
)

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
	// Icon is a small PNG drawn to the left of the label, in the same encoding
	// as the tray's own icon (see [New]). Nil leaves the row text-only.
	//
	// It is PNG bytes rather than an image.Image for the same reason the tray
	// icon is: a caller ships one artefact that every backend decodes, instead
	// of each backend agreeing on a pixel layout with the caller.
	//
	// A backend scales it to the height its platform draws menu rows at and
	// keeps the aspect ratio, so an icon does not have to be authored at a
	// particular size -- extra pixels become resolution, not dimensions. A
	// monochrome glyph is drawn as a TEMPLATE (see [IsTemplate]) and so follows
	// a light or dark menu; one that carries colour keeps its colour.
	//
	// Honoured today by the macOS backend. The Windows and Linux backends
	// ignore it, and a row that carries one there simply draws as it did
	// before -- the field is not a promise those platforms have kept yet.
	Icon []byte
	// Key is the row's key equivalent -- the ONE CHARACTER the platform draws
	// at the right of the row, aligned in a column with every other row's.
	//
	// A character and not a name: "s" for S, "=" for the equals key. The keys
	// with no character of their own have constants -- [KeyUp], [KeyDown],
	// [KeyLeft], [KeyRight], [KeyReturn], [KeyEscape], [KeyDelete] -- because a
	// menu draws an arrow there, not the word "Up".
	//
	// ⚠ IT IS DRAWN, AND ON macOS IT IS ALSO BOUND, but only where a menu's key
	// equivalents are ever consulted: the main menu, and a menu while it is
	// open. A tray menu is neither for as long as it is shut, so for a status
	// item this is display. An application that also owns a main menu should
	// expect the combination to work there too.
	//
	// Honoured today by the macOS backend. The Windows and Linux backends ignore
	// it, and a row that carries one there draws as it did before.
	Key string
	// Mods are the modifiers shown with [MenuItem.Key]. Zero draws the character
	// alone, which is what a bare key equivalent looks like.
	Mods Mods
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

// IconItem is a clickable menu item carrying a PNG icon; see [MenuItem.Icon].
func IconItem(label string, iconPNG []byte, onClick func()) *MenuItem {
	return &MenuItem{Label: label, Icon: iconPNG, OnClick: onClick}
}

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
//
// What it SHOWS -- icon, tooltip, menu -- is read by the platform loop and
// written by whoever changes it, and those are not the same goroutine: an icon
// bound to application state (see [BindIcon]) is written by a ticker while the
// loop is drawing. So the three are behind a lock. The backend and the ready
// callback are not: they are set while the tray is being built, before anything
// runs, and a tray whose backend changed underneath a running loop would be a
// different bug entirely.
type Tray struct {
	mu      sync.RWMutex
	icon    []byte // PNG bytes
	title   string
	tooltip string
	menu    *Menu
	visible bool
	backend Backend
	onReady func()
}

// New creates a tray showing iconPNG (PNG-encoded bytes). The platform backend
// is selected automatically; use WithBackend to override (eg. for tests). It
// starts visible — see SetVisible.
func New(iconPNG []byte) *Tray {
	return &Tray{icon: iconPNG, menu: NewMenu(), visible: true, backend: defaultBackend()}
}

// WithBackend overrides the platform backend and returns the tray.
func (t *Tray) WithBackend(b Backend) *Tray { t.backend = b; return t }

// SetTooltip sets the hover tooltip and refreshes if running.
func (t *Tray) SetTooltip(s string) *Tray {
	t.mu.Lock()
	t.tooltip = s
	t.mu.Unlock()
	// Refreshed OUTSIDE the lock: a backend reads the tray back to re-apply it,
	// and reading it while still holding the write lock is a deadlock.
	t.refresh()
	return t
}

// SetIcon replaces the icon (PNG bytes) and refreshes if running.
func (t *Tray) SetIcon(iconPNG []byte) *Tray {
	t.mu.Lock()
	t.icon = iconPNG
	t.mu.Unlock()
	t.refresh()
	return t
}

// SetTitle sets text drawn directly in the menu bar alongside (or instead
// of) the icon — the way a system meter shows "42%" rather than only a
// glyph — and refreshes if running. An empty title leaves only the icon,
// which is also SetTitle's zero-value behavior on a Tray nobody has called
// it on.
//
// Honoured today by the macOS backend, which owns real screen space for it
// (the button holds both an image and a title). The Windows and Linux
// backends ignore it, the same partial-platform-support shape
// [MenuItem.Icon] already has: a caller on those platforms simply sees
// what it drew before, not an error.
func (t *Tray) SetTitle(s string) *Tray {
	t.mu.Lock()
	t.title = s
	t.mu.Unlock()
	t.refresh()
	return t
}

// SetVisible shows or hides the tray's own icon without releasing the
// platform loop it may be Holding for other items Attached to it (e.g.
// go-aiquota/tray/menubar's control item, whose Hold is what every
// per-account item's Attach joins — closing or not-Run-ning it would take
// the whole loop down with it, which hiding does not). Honoured today by
// the macOS backend (NSStatusItem.visible); other backends ignore it, the
// same partial-platform-support shape SetTitle already has.
func (t *Tray) SetVisible(v bool) *Tray {
	t.mu.Lock()
	t.visible = v
	t.mu.Unlock()
	t.refresh()
	return t
}

// SetMenu sets the tray menu and refreshes if running.
func (t *Tray) SetMenu(m *Menu) *Tray {
	t.mu.Lock()
	t.menu = m
	t.mu.Unlock()
	t.refresh()
	return t
}

// OnReady registers a callback run once the tray is live (after Run starts).
func (t *Tray) OnReady(fn func()) *Tray { t.onReady = fn; return t }

// Accessors used by backends, safe to call from the platform loop while another
// goroutine is setting them.
func (t *Tray) Icon() []byte {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.icon
}

func (t *Tray) Title() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.title
}

func (t *Tray) Tooltip() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tooltip
}

func (t *Tray) Menu() *Menu {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.menu
}

func (t *Tray) Visible() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.visible
}

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

// attacher is the optional Backend capability to add the tray to a run loop the
// HOST already owns, instead of starting (and blocking on) its own. A backend
// implements it when a host application with its own platform event loop — e.g.
// a GUI app that already runs [NSApp run] for its window — wants a tray icon
// too. Backends that can't attach (headless, or a platform without native
// support) simply don't implement it, and Attach reports ErrNoBackend.
type attacher interface {
	Attach(t *Tray) error
}

// Attach shows the tray inside a host-owned event loop and returns immediately,
// instead of Run's block-until-Quit. Use it from an application that already
// drives the platform's main run loop (its own window): Run would try to start
// a second loop, whereas Attach just registers the tray with the running one.
// It must be called on the platform's main/UI thread. Returns ErrNoBackend when
// the active backend does not support attaching.
func (t *Tray) Attach() error {
	if t.backend == nil {
		return ErrNoBackend
	}
	if a, ok := t.backend.(attacher); ok {
		return a.Attach(t)
	}
	return ErrNoBackend
}

// Quit stops the tray's event loop.
func (t *Tray) Quit() {
	if t.backend != nil {
		t.backend.Quit()
	}
}

// remover is the optional Backend capability to take THIS ONE item out of
// the bar without stopping the shared platform loop any other Attached (or
// the Holding) Tray depends on — the counterpart Close needs to Quit's
// whole-loop behavior. A backend without finer-grained removal (Headless;
// Windows and Linux today) simply doesn't implement it, and Close falls
// back to Quit — the same partial-platform-support shape [MenuItem.Icon]
// and [Tray.SetTitle] already have.
type remover interface {
	Remove(t *Tray)
}

// Close removes this tray's item from the menu bar, for a Tray that was
// Attached (not Run) and whose life ends before the process's own — e.g.
// one of several per-account items in a host that keeps running after one
// account is removed. Unlike Quit, it does not stop the platform loop: a
// caller with several Attached items (and one Holding the loop) can Close
// any of the Attached ones without taking the others down.
//
// On a backend with no such capability, Close falls back to Quit, which is
// at least as safe as doing nothing when there is only one item running the
// whole loop by itself.
func (t *Tray) Close() {
	if t.backend == nil {
		return
	}
	if r, ok := t.backend.(remover); ok {
		r.Remove(t)
		return
	}
	t.backend.Quit()
}

func (t *Tray) refresh() {
	if t.backend != nil {
		t.backend.Refresh(t)
	}
}
