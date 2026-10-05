//go:build linux

package tray

// Linux system-tray backend: a freedesktop StatusNotifierItem (the modern
// replacement for the XEmbed system tray, understood by KDE, GNOME's
// AppIndicator extension, sway/waybar, etc.) whose menu is exported over
// com.canonical.dbusmenu. Pure Go on top of github.com/godbus/dbus/v5 — no CGO.
//
// The bus-facing methods are tested in-process (native_linux_test.go) against a
// connection with no bus behind it. Registration itself needs a live session bus
// and a StatusNotifierWatcher (a running desktop shell), which headless CI
// cannot provide.
//
// Flow: Run (or Attach, which returns instead of blocking) connects to the session bus, exports the SNI object (with its icon
// as an ARGB IconPixmap), exports a dbusmenu built from the tray's Menu, claims
// a well-known name and registers it with org.kde.StatusNotifierWatcher, then
// blocks until Quit. A dbusmenu Event(id,"clicked",...) maps the node id back to
// a *MenuItem and calls Activate.

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	sniPath      = "/StatusNotifierItem"
	menuPath     = "/MenuBar"
	sniIface     = "org.kde.StatusNotifierItem"
	menuIface    = "com.canonical.dbusmenu"
	watcherName  = "org.kde.StatusNotifierWatcher"
	watcherPath  = "/StatusNotifierWatcher"
	watcherIface = "org.kde.StatusNotifierWatcher"
)

// pixmap is the SNI IconPixmap wire element (width, height, ARGB32 bytes),
// marshalling to the DBus struct signature "(iiay)".
type pixmap struct {
	Width  int32
	Height int32
	Bytes  []byte
}

// menuLayout is the dbusmenu GetLayout node ("(ia{sv}av)"): an id, its
// properties, and its children (each a variant wrapping another menuLayout).
type menuLayout struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

// groupProp is one element of dbusmenu GetGroupProperties ("(ia{sv})").
type groupProp struct {
	ID    int32
	Props map[string]dbus.Variant
}

// menuNode is one entry in the flattened dbusmenu tree. Node id 0 is the
// synthetic root; real items get ids 1..N.
type menuNode struct {
	item     *MenuItem // nil for the root
	children []int
}

// linuxBackend owns the DBus connection and the exported SNI + dbusmenu objects.
//
// ⛔ TWO SIDES TOUCH IT. Refresh runs on whoever changed the tray (SetMenu,
// SetIcon, a bound icon's ticker); GetLayout, GetGroupProperties and Event run
// on godbus's goroutines whenever the desktop shell asks, which is exactly when
// somebody opens the menu. So the flattened tree and its revision -- and the
// connection and properties Refresh reaches through -- are behind mu, the same
// way the Tray keeps what it shows behind its own (go-widgets/tray#35).
type linuxBackend struct {
	tray *Tray

	mu        sync.Mutex
	conn      *dbus.Conn
	tree      []menuNode
	rev       uint32
	sniProps  *prop.Properties
	menuProps *prop.Properties

	done     chan struct{} // closed by Quit; guarded by mu
	quitting bool          // Quit was called, possibly before done existed
	quitOnce sync.Once
}

// defaultBackend links the StatusNotifierItem backend.
func defaultBackend() Backend { return &linuxBackend{} }

func (b *linuxBackend) Run(t *Tray) error {
	runtime.LockOSThread()
	done, err := b.start(t)
	if err != nil {
		return err
	}
	t.ready()
	<-done
	b.connection().Close()
	return nil
}

// Attach implements the attacher capability (see [Tray.Attach]): the same
// StatusNotifierItem Run puts up, returned from as soon as it is registered.
//
// There is no host loop to join on Linux, which is what makes this cheap: the
// item is a set of objects exported on the session bus, and godbus serves them
// from goroutines of its own whoever called. So a host that owns its window's
// loop -- go-widgets/application -- gets a tray without giving a goroutine to
// Run. Quit (or Close, which falls back to it here) releases the bus name and
// closes the connection, and the shell drops the item.
//
// Like the darwin backend's Attach it does not fire OnReady: the host's own
// loop is what is ready, and it already said so.
func (b *linuxBackend) Attach(t *Tray) error {
	done, err := b.start(t)
	if err != nil {
		return err
	}
	go func() {
		<-done
		b.connection().Close()
	}()
	return nil
}

// start connects to the session bus, exports the item and its menu, and
// registers with the watcher. A failure closes what it opened, so neither Run
// nor Attach leaves a half-exported item on the bus.
func (b *linuxBackend) start(t *Tray) (done chan struct{}, err error) {
	b.tray = t
	done = b.newDone()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			b.setConn(nil) // Refresh then does nothing, rather than emit on a closed connection
			conn.Close()
		}
	}()
	b.setConn(conn)
	b.rebuild()

	// Method objects.
	if err := conn.Export(&statusNotifierItem{b: b}, sniPath, sniIface); err != nil {
		return nil, err
	}
	if err := conn.Export(&dbusMenu{b: b}, menuPath, menuIface); err != nil {
		return nil, err
	}

	// Properties.
	if err := b.exportProps(conn, t); err != nil {
		return nil, err
	}

	// Introspection so hosts can discover the interfaces.
	conn.Export(introspect.NewIntrospectable(sniIntrospect), sniPath, "org.freedesktop.DBus.Introspectable")
	conn.Export(introspect.NewIntrospectable(menuIntrospect), menuPath, "org.freedesktop.DBus.Introspectable")

	// Claim a per-process well-known name and register with the watcher.
	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	if _, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil {
		return nil, err
	}
	watcher := conn.Object(watcherName, dbus.ObjectPath(watcherPath))
	watcher.Call(watcherIface+".RegisterStatusNotifierItem", 0, name)
	return done, nil
}

// setConn and connection are the connection's guarded accessors: Run and Attach
// set it on one goroutine while Refresh may read it on another.
func (b *linuxBackend) setConn(c *dbus.Conn) {
	b.mu.Lock()
	b.conn = c
	b.mu.Unlock()
}

func (b *linuxBackend) connection() *dbus.Conn {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn
}

func (b *linuxBackend) Refresh(t *Tray) {
	b.mu.Lock()
	conn, sniProps := b.conn, b.sniProps
	b.mu.Unlock()
	if conn == nil {
		return
	}
	rev := b.rebuild()
	if sniProps != nil {
		sniProps.SetMust(sniIface, "Title", t.Tooltip())
		sniProps.SetMust(sniIface, "IconPixmap", []pixmap{b.iconPixmap(t.Icon())})
	}
	conn.Emit(dbus.ObjectPath(sniPath), sniIface+".NewIcon")
	conn.Emit(dbus.ObjectPath(sniPath), sniIface+".NewTitle")
	conn.Emit(dbus.ObjectPath(menuPath), menuIface+".LayoutUpdated", rev, int32(0))
}

// Quit releases the item. It may arrive before Run or Attach has made the
// channel it closes -- application calls Attach on a goroutine and Quit when its
// window returns -- so it is remembered, and start honours it.
func (b *linuxBackend) Quit() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.quitting = true
	if b.done != nil {
		b.quitOnce.Do(func() { close(b.done) })
	}
}

// newDone makes the channel Quit closes, already closed when Quit came first.
func (b *linuxBackend) newDone() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.done = make(chan struct{})
	if b.quitting {
		b.quitOnce.Do(func() { close(b.done) })
	}
	return b.done
}

// rebuild flattens the tray's Menu into a new tree (node id == index), swaps it
// in and bumps the dbusmenu revision, which it returns.
//
// The tree is built in a FRESH slice and swapped under the lock, never edited in
// place: the old code truncated the live slice and appended to it, so a
// GetLayout that had already read it walked a tree that was being rewritten
// under it.
func (b *linuxBackend) rebuild() uint32 {
	tree := []menuNode{{}} // synthetic root, id 0
	var add func(parent int, m *Menu)
	add = func(parent int, m *Menu) {
		if m == nil {
			return
		}
		for _, it := range m.Items {
			id := len(tree)
			tree = append(tree, menuNode{item: it})
			tree[parent].children = append(tree[parent].children, id)
			if it.Submenu != nil {
				add(id, it.Submenu)
			}
		}
	}
	add(0, b.tray.Menu())

	b.mu.Lock()
	defer b.mu.Unlock()
	b.tree = tree
	b.rev++
	return b.rev
}

// iconPixmap converts the tray icon to an ARGB32 SNI pixmap (zero-sized if none).
func (b *linuxBackend) iconPixmap(png []byte) pixmap {
	rgba, w, h, err := pngPixels(png)
	if err != nil || w == 0 || h == 0 {
		return pixmap{}
	}
	return pixmap{Width: int32(w), Height: int32(h), Bytes: toARGB(rgba)}
}

func (b *linuxBackend) exportProps(conn *dbus.Conn, t *Tray) error {
	px := b.iconPixmap(t.Icon())
	sniSpec := map[string]map[string]*prop.Prop{
		sniIface: {
			"Category":   {Value: "ApplicationStatus", Emit: prop.EmitTrue},
			"Id":         {Value: "go-widgets-tray", Emit: prop.EmitTrue},
			"Title":      {Value: t.Tooltip(), Emit: prop.EmitTrue},
			"Status":     {Value: "Active", Emit: prop.EmitTrue},
			"IconName":   {Value: "", Emit: prop.EmitTrue},
			"IconPixmap": {Value: []pixmap{px}, Emit: prop.EmitTrue},
			"ItemIsMenu": {Value: true, Emit: prop.EmitTrue},
			"Menu":       {Value: dbus.ObjectPath(menuPath), Emit: prop.EmitTrue},
		},
	}
	p, err := prop.Export(conn, sniPath, sniSpec)
	if err != nil {
		return err
	}

	menuSpec := map[string]map[string]*prop.Prop{
		menuIface: {
			"Version":       {Value: uint32(4), Emit: prop.EmitTrue},
			"Status":        {Value: "normal", Emit: prop.EmitTrue},
			"TextDirection": {Value: "ltr", Emit: prop.EmitTrue},
			"IconThemePath": {Value: []string{}, Emit: prop.EmitTrue},
		},
	}
	mp, err := prop.Export(conn, menuPath, menuSpec)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.sniProps, b.menuProps = p, mp
	b.mu.Unlock()
	return nil
}

// statusNotifierItem implements the org.kde.StatusNotifierItem methods. The menu
// is served separately over dbusmenu, so the activation methods are no-ops.
type statusNotifierItem struct{ b *linuxBackend }

func (s *statusNotifierItem) ContextMenu(x, y int32) *dbus.Error       { return nil }
func (s *statusNotifierItem) Activate(x, y int32) *dbus.Error          { return nil }
func (s *statusNotifierItem) SecondaryActivate(x, y int32) *dbus.Error { return nil }
func (s *statusNotifierItem) Scroll(delta int32, orientation string) *dbus.Error {
	return nil
}

// dbusMenu implements the com.canonical.dbusmenu methods the tray needs.
type dbusMenu struct{ b *linuxBackend }

// GetLayout returns the (sub)tree rooted at parentID, and the revision it
// belongs to, read together under the lock so the two cannot disagree.
func (d *dbusMenu) GetLayout(parentID, recursionDepth int32, propertyNames []string) (uint32, menuLayout, *dbus.Error) {
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	return d.b.rev, d.b.buildLayout(int(parentID)), nil
}

// GetGroupProperties returns the properties for a set of node ids.
func (d *dbusMenu) GetGroupProperties(ids []int32, propertyNames []string) ([]groupProp, *dbus.Error) {
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	out := make([]groupProp, 0, len(ids))
	for _, id := range ids {
		if int(id) >= 0 && int(id) < len(d.b.tree) {
			out = append(out, groupProp{ID: id, Props: itemProps(d.b.tree[id].item)})
		}
	}
	return out, nil
}

// Event dispatches a "clicked" event to the matching *MenuItem.
//
// The item is looked up under the lock and activated OUTSIDE it: OnClick is
// application code, and the natural thing for it to do is change the menu,
// whose Refresh takes the same lock.
func (d *dbusMenu) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	var it *MenuItem
	d.b.mu.Lock()
	if int(id) >= 0 && int(id) < len(d.b.tree) {
		it = d.b.tree[id].item
	}
	d.b.mu.Unlock()
	if it != nil {
		it.Activate()
	}
	return nil
}

// AboutToShow reports whether the layout must be refreshed before display; the
// tray keeps its tree current, so nothing changes.
func (d *dbusMenu) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }

// buildLayout renders the node with the given id (and its descendants) into a
// dbusmenu layout struct. The caller holds b.mu.
func (b *linuxBackend) buildLayout(id int) menuLayout {
	if id < 0 || id >= len(b.tree) {
		return menuLayout{ID: int32(id), Props: map[string]dbus.Variant{}}
	}
	n := b.tree[id]
	out := menuLayout{ID: int32(id), Props: itemProps(n.item)}
	for _, c := range n.children {
		out.Children = append(out.Children, dbus.MakeVariant(b.buildLayout(c)))
	}
	return out
}

// itemProps maps a *MenuItem (nil == the root) to dbusmenu properties.
func itemProps(it *MenuItem) map[string]dbus.Variant {
	p := map[string]dbus.Variant{}
	if it == nil {
		p["children-display"] = dbus.MakeVariant("submenu")
		return p
	}
	if it.Separator {
		p["type"] = dbus.MakeVariant("separator")
		return p
	}
	p["label"] = dbus.MakeVariant(it.Label)
	p["enabled"] = dbus.MakeVariant(!it.Disabled)
	p["visible"] = dbus.MakeVariant(true)
	if it.Submenu != nil {
		p["children-display"] = dbus.MakeVariant("submenu")
	}
	if it.checkbox {
		p["toggle-type"] = dbus.MakeVariant("checkmark")
		state := int32(0)
		if it.Checked {
			state = 1
		}
		p["toggle-state"] = dbus.MakeVariant(state)
	}
	return p
}

// Introspection data advertising the interfaces the backend exports.
var sniIntrospect = &introspect.Node{
	Name: sniPath,
	Interfaces: []introspect.Interface{
		introspect.IntrospectData,
		prop.IntrospectData,
		{
			Name: sniIface,
			Methods: []introspect.Method{
				{Name: "ContextMenu", Args: []introspect.Arg{{Name: "x", Type: "i", Direction: "in"}, {Name: "y", Type: "i", Direction: "in"}}},
				{Name: "Activate", Args: []introspect.Arg{{Name: "x", Type: "i", Direction: "in"}, {Name: "y", Type: "i", Direction: "in"}}},
				{Name: "SecondaryActivate", Args: []introspect.Arg{{Name: "x", Type: "i", Direction: "in"}, {Name: "y", Type: "i", Direction: "in"}}},
				{Name: "Scroll", Args: []introspect.Arg{{Name: "delta", Type: "i", Direction: "in"}, {Name: "orientation", Type: "s", Direction: "in"}}},
			},
			Signals: []introspect.Signal{
				{Name: "NewIcon"},
				{Name: "NewTitle"},
				{Name: "NewStatus", Args: []introspect.Arg{{Name: "status", Type: "s"}}},
			},
			Properties: []introspect.Property{
				{Name: "Category", Type: "s", Access: "read"},
				{Name: "Id", Type: "s", Access: "read"},
				{Name: "Title", Type: "s", Access: "read"},
				{Name: "Status", Type: "s", Access: "read"},
				{Name: "IconName", Type: "s", Access: "read"},
				{Name: "IconPixmap", Type: "a(iiay)", Access: "read"},
				{Name: "ItemIsMenu", Type: "b", Access: "read"},
				{Name: "Menu", Type: "o", Access: "read"},
			},
		},
	},
}

var menuIntrospect = &introspect.Node{
	Name: menuPath,
	Interfaces: []introspect.Interface{
		introspect.IntrospectData,
		prop.IntrospectData,
		{
			Name: menuIface,
			Methods: []introspect.Method{
				{Name: "GetLayout", Args: []introspect.Arg{
					{Name: "parentId", Type: "i", Direction: "in"},
					{Name: "recursionDepth", Type: "i", Direction: "in"},
					{Name: "propertyNames", Type: "as", Direction: "in"},
					{Name: "revision", Type: "u", Direction: "out"},
					{Name: "layout", Type: "(ia{sv}av)", Direction: "out"},
				}},
				{Name: "GetGroupProperties", Args: []introspect.Arg{
					{Name: "ids", Type: "ai", Direction: "in"},
					{Name: "propertyNames", Type: "as", Direction: "in"},
					{Name: "properties", Type: "a(ia{sv})", Direction: "out"},
				}},
				{Name: "Event", Args: []introspect.Arg{
					{Name: "id", Type: "i", Direction: "in"},
					{Name: "eventId", Type: "s", Direction: "in"},
					{Name: "data", Type: "v", Direction: "in"},
					{Name: "timestamp", Type: "u", Direction: "in"},
				}},
				{Name: "AboutToShow", Args: []introspect.Arg{
					{Name: "id", Type: "i", Direction: "in"},
					{Name: "needUpdate", Type: "b", Direction: "out"},
				}},
			},
			Signals: []introspect.Signal{
				{Name: "LayoutUpdated", Args: []introspect.Arg{{Name: "revision", Type: "u"}, {Name: "parent", Type: "i"}}},
				{Name: "ItemsPropertiesUpdated"},
			},
			Properties: []introspect.Property{
				{Name: "Version", Type: "u", Access: "read"},
				{Name: "Status", Type: "s", Access: "read"},
				{Name: "TextDirection", Type: "s", Access: "read"},
				{Name: "IconThemePath", Type: "as", Access: "read"},
			},
		},
	},
}
