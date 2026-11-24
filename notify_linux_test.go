package tray

import (
	"errors"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type fakeBus struct {
	caps       []string
	capsErr    error
	notifyErr  error
	nextID     uint32
	gotSummary string
	gotBody    string
	gotActions []string
}

func (f *fakeBus) capabilities() ([]string, error) { return f.caps, f.capsErr }
func (f *fakeBus) notify(s, b string, a []string) (uint32, error) {
	f.gotSummary, f.gotBody, f.gotActions = s, b, a
	return f.nextID, f.notifyErr
}

func TestPostNotificationEscapesOnlyForMarkupServers(t *testing.T) {
	n := Notification{Title: "t", Body: `a <b> & "c"`}
	markup := &fakeBus{caps: []string{"actions", "body-markup"}, nextID: 1}
	if err := postNotification(markup, newClickTable[uint32](), n); err != nil {
		t.Fatal(err)
	}
	if markup.gotBody != "a &lt;b&gt; &amp; &#34;c&#34;" {
		t.Fatalf("markup server got %q", markup.gotBody)
	}
	plain := &fakeBus{caps: []string{"actions"}, nextID: 2}
	postNotification(plain, newClickTable[uint32](), n)
	if plain.gotBody != n.Body {
		t.Fatalf("plain server got %q", plain.gotBody)
	}
	unknown := &fakeBus{capsErr: errors.New("no GetCapabilities"), nextID: 3}
	postNotification(unknown, newClickTable[uint32](), n)
	if unknown.gotBody != n.Body {
		t.Fatalf("a server that cannot say got %q", unknown.gotBody)
	}
}

func TestPostNotificationActionsOnlyWithOnClick(t *testing.T) {
	b := &fakeBus{nextID: 7}
	clicks := newClickTable[uint32]()
	postNotification(b, clicks, Notification{Title: "t"})
	if len(b.gotActions) != 0 || clicks.take(7) != nil {
		t.Fatalf("no OnClick: actions %v", b.gotActions)
	}
	postNotification(b, clicks, Notification{Title: "t", OnClick: func() {}})
	if !slices.Equal(b.gotActions, []string{"default", "Open"}) || clicks.take(7) == nil {
		t.Fatalf("OnClick: actions %v", b.gotActions)
	}
}

func TestPostNotificationBusError(t *testing.T) {
	boom := errors.New("boom")
	b := &fakeBus{notifyErr: boom, nextID: 9}
	clicks := newClickTable[uint32]()
	err := postNotification(b, clicks, Notification{Title: "t", OnClick: func() {}})
	if !errors.Is(err, boom) || clicks.take(9) != nil {
		t.Fatalf("err = %v, and a failed post must leave no callback", err)
	}
}

func sig(name string, body ...any) *dbus.Signal {
	return &dbus.Signal{Path: dbus.ObjectPath(notifPath), Name: notifIface + "." + name, Body: body}
}

func TestHandleSignal(t *testing.T) {
	clicks := newClickTable[uint32]()
	var ran atomic.Int32
	reg := func(id uint32) { clicks.add(id, func() { ran.Add(1) }) }

	reg(1)
	handleSignal(clicks, sig("ActionInvoked", uint32(1), "default"))
	deadline := time.Now().Add(2 * time.Second)
	for ran.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if ran.Load() != 1 {
		t.Fatal("default action did not run the callback")
	}
	if clicks.take(1) != nil {
		t.Fatal("callback not consumed")
	}

	// Not the default action, a closed notification, and malformed or foreign
	// signals must not run anything (and a closure forgets the callback).
	reg(2)
	handleSignal(clicks, sig("ActionInvoked", uint32(2), "other"))
	handleSignal(clicks, sig("ActionInvoked", uint32(2)))      // short body
	handleSignal(clicks, sig("ActionInvoked", "2", "default")) // wrong type
	handleSignal(clicks, sig("ActionInvoked"))                 // empty body
	handleSignal(clicks, nil)                                  // nil
	handleSignal(clicks, &dbus.Signal{Path: "/elsewhere", Name: notifIface + ".ActionInvoked", Body: []any{uint32(2), "default"}})
	handleSignal(clicks, sig("Unknown", uint32(2)))
	time.Sleep(20 * time.Millisecond)
	if ran.Load() != 1 {
		t.Fatalf("ran = %d: something ran that must not", ran.Load())
	}
	handleSignal(clicks, sig("NotificationClosed", uint32(2), uint32(2)))
	if clicks.take(2) != nil {
		t.Fatal("NotificationClosed must forget the callback")
	}
}

func TestLinuxNotifyNotRunning(t *testing.T) {
	b := &linuxBackend{}
	if err := b.Notify(nil, Notification{Title: "t"}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

func FuzzHandleSignal(f *testing.F) {
	f.Add("ActionInvoked", uint32(1), "default")
	f.Add("NotificationClosed", uint32(1), "")
	f.Fuzz(func(t *testing.T, name string, id uint32, key string) {
		c := newClickTable[uint32]()
		c.add(id, func() {})
		handleSignal(c, sig(name, id, key)) // must not panic
		handleSignal(c, sig(name, key, id))
	})
}

// fakeDaemon is a notification server exported on a real bus, so the test below
// exercises the wire encoding (argument types, signal shapes) and not only the
// Go logic over the notifyBus seam.
type fakeDaemon struct {
	got chan []any
}

func (d *fakeDaemon) GetCapabilities() ([]string, *dbus.Error) {
	return []string{"actions", "body-markup"}, nil
}

func (d *fakeDaemon) Notify(app string, replaces uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	d.got <- []any{app, replaces, icon, summary, body, actions, timeout}
	return 42, nil
}

// TestNotifyOverARealBus needs a session bus (dbus-run-session); it is skipped
// where there is none, as on a developer laptop without one.
func TestNotifyOverARealBus(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("no session bus")
	}
	srv, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Skip(err)
	}
	defer srv.Close()
	d := &fakeDaemon{got: make(chan []any, 1)}
	if err := srv.Export(d, notifPath, notifIface); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.RequestName(notifName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}

	cli, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	b := &linuxBackend{}
	b.setConn(cli)

	var clicked atomic.Bool
	if err := b.Notify(nil, Notification{Title: "Tunnel dropped", Body: "a <b>", OnClick: func() { clicked.Store(true) }}); err != nil {
		t.Fatal(err)
	}
	got := <-d.got
	if got[3] != "Tunnel dropped" || got[4] != "a &lt;b&gt;" || !slices.Equal(got[5].([]string), []string{"default", "Open"}) || got[6] != int32(-1) {
		t.Fatalf("daemon received %v", got)
	}

	if err := srv.Emit(dbus.ObjectPath(notifPath), notifIface+".ActionInvoked", uint32(42), "default"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !clicked.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !clicked.Load() {
		t.Fatal("the ActionInvoked signal did not reach OnClick")
	}
}
