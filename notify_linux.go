package tray

// Linux notifications: org.freedesktop.Notifications (the Desktop Notifications
// Specification 1.2), spoken over the tray's own session-bus connection. Pure
// Go, no CGO, no libnotify.
//
// The bus is behind the small notifyBus interface so the whole flow -- escaping,
// action wiring, the click table -- is tested without a notification daemon.

import (
	"errors"
	"html"
	"os"
	"path/filepath"
	"slices"

	"github.com/godbus/dbus/v5"
)

const (
	notifName  = "org.freedesktop.Notifications"
	notifPath  = "/org/freedesktop/Notifications"
	notifIface = "org.freedesktop.Notifications"
)

// notifyBus is the part of the notification service this file uses.
type notifyBus interface {
	capabilities() ([]string, error)
	// notify posts the notification and returns the server's id for it.
	notify(summary, body string, actions []string) (uint32, error)
}

type dbusNotifyBus struct{ conn *dbus.Conn }

func (d dbusNotifyBus) obj() dbus.BusObject {
	return d.conn.Object(notifName, dbus.ObjectPath(notifPath))
}

func (d dbusNotifyBus) capabilities() ([]string, error) {
	var caps []string
	err := d.obj().Call(notifIface+".GetCapabilities", 0).Store(&caps)
	return caps, err
}

func (d dbusNotifyBus) notify(summary, body string, actions []string) (uint32, error) {
	if actions == nil {
		actions = []string{}
	}
	var id uint32
	err := d.obj().Call(notifIface+".Notify", 0,
		filepath.Base(os.Args[0]), uint32(0), "", summary, body, actions,
		map[string]dbus.Variant{}, int32(-1)).Store(&id)
	return id, err
}

// postNotification is the platform-neutral half of Notify, over any notifyBus.
func postNotification(bus notifyBus, clicks *clickTable[uint32], n Notification) error {
	body := n.Body
	// The body of a notification is markup when the server says so. Escaping what
	// the caller meant as text keeps a "<" in an error message from being read as
	// a tag; a server without the capability gets the text as it is.
	if caps, err := bus.capabilities(); err == nil && slices.Contains(caps, "body-markup") {
		body = html.EscapeString(body)
	}
	var actions []string
	if n.OnClick != nil {
		// "default" is the action a plain click on the notification invokes.
		actions = []string{"default", "Open"}
	}
	id, err := bus.notify(n.Title, body, actions)
	if err != nil {
		return err
	}
	if n.OnClick != nil {
		clicks.add(id, n.OnClick)
	}
	return nil
}

// handleSignal consumes one bus signal: ActionInvoked(u id, s key) runs the
// callback, NotificationClosed(u id, u reason) forgets it. Anything else, or a
// body of the wrong shape from a hostile peer, is ignored.
func handleSignal(c *clickTable[uint32], sig *dbus.Signal) {
	if sig == nil || sig.Path != dbus.ObjectPath(notifPath) || len(sig.Body) < 1 {
		return
	}
	id, ok := sig.Body[0].(uint32)
	if !ok {
		return
	}
	switch sig.Name {
	case notifIface + ".ActionInvoked":
		if len(sig.Body) < 2 {
			return
		}
		if key, _ := sig.Body[1].(string); key != "default" {
			return
		}
		if fn := c.take(id); fn != nil {
			go fn()
		}
	case notifIface + ".NotificationClosed":
		c.take(id)
	}
}

// notifier returns the click table for conn, subscribing to the service's
// signals the first time a connection is seen.
func (b *linuxBackend) notifier(conn *dbus.Conn) (*clickTable[uint32], error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.clicks != nil && b.clicksConn == conn {
		return b.clicks, nil
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(dbus.ObjectPath(notifPath)),
		dbus.WithMatchInterface(notifIface),
	); err != nil {
		return nil, err
	}
	table := newClickTable[uint32]()
	ch := make(chan *dbus.Signal, 16)
	conn.Signal(ch)
	go func() {
		for sig := range ch {
			handleSignal(table, sig)
		}
	}()
	b.clicks, b.clicksConn = table, conn
	return table, nil
}

// Notify implements [Notifier].
func (b *linuxBackend) Notify(_ *Tray, n Notification) error {
	conn := b.connection()
	if conn == nil {
		return ErrNotRunning
	}
	clicks, err := b.notifier(conn)
	if err != nil {
		return errors.Join(ErrNoNotifier, err)
	}
	return postNotification(dbusNotifyBus{conn}, clicks, n)
}
