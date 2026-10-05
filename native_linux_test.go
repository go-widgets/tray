// Copyright (c) 2026 the go-widgets/tray authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

//go:build linux

package tray

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// pipeConn is a *dbus.Conn whose far end is drained and dropped: enough for
// Refresh to emit its signals, with no session bus behind it.
//
// godbus does not start reading, or authenticating, until it is asked to, so a
// connection made with NewConn over one end of a pipe only ever WRITES -- and a
// pipe whose other end is read to io.Discard never blocks a writer.
func pipeConn(t *testing.T) *dbus.Conn {
	t.Helper()
	near, far := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, far) }()
	conn, err := dbus.NewConn(near)
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = far.Close() })
	return conn
}

// TestTheMenuTreeCanBeRebuiltWhileTheBusReadsIt is a race regression
// (go-widgets/tray#35).
//
// Refresh runs on whoever changed the tray -- SetMenu, SetIcon, a bound icon's
// ticker -- and rebuilds the flattened dbusmenu tree. GetLayout,
// GetGroupProperties and Event run on godbus's goroutines whenever the desktop
// shell asks, which is exactly when the menu is opened. They read that tree, and
// nothing ordered the two: the shared slice was truncated and appended to while
// a GetLayout walked it.
//
// It fails only under -race, which is what CI's test lane runs.
func TestTheMenuTreeCanBeRebuiltWhileTheBusReadsIt(t *testing.T) {
	menu := func() *Menu {
		return NewMenu().Add(
			Item("Open", nil),
			Checkbox("Notify", true, nil),
			SubMenu("More", NewMenu().Add(Item("Deep", nil), Separator())),
		)
	}
	tr := New(nil).WithBackend(NewHeadless()).SetMenu(menu())
	b := &linuxBackend{tray: tr}
	b.conn = pipeConn(t)
	b.rebuild()
	d := &dbusMenu{b: b}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // the application side
		defer wg.Done()
		for range 200 {
			tr.SetMenu(menu())
			b.Refresh(tr)
		}
	}()
	go func() { // the bus side
		defer wg.Done()
		for range 200 {
			_, _, _ = d.GetLayout(0, -1, nil)
			_, _ = d.GetGroupProperties([]int32{0, 1, 2, 3, 4}, nil)
			_ = d.Event(1, "hovered", dbus.MakeVariant(""), 0)
		}
	}()
	wg.Wait()

	rev, layout, derr := d.GetLayout(0, -1, nil)
	if derr != nil {
		t.Fatalf("GetLayout: %v", derr)
	}
	if rev == 0 {
		t.Error("the revision never moved, so a shell would never re-read the menu")
	}
	if len(layout.Children) != 3 {
		t.Errorf("the root has %d children after the rebuilds, want the menu's 3", len(layout.Children))
	}
}

// TestAClickIsDispatchedOutsideTheTreeLock: an item's OnClick is application
// code, and the obvious thing for it to do is change the menu -- which rebuilds
// the tree. Holding the tree's lock while it runs would deadlock on that first
// click.
func TestAClickIsDispatchedOutsideTheTreeLock(t *testing.T) {
	tr := New(nil).WithBackend(NewHeadless())
	b := &linuxBackend{tray: tr}
	b.conn = pipeConn(t)
	clicked := false
	tr.SetMenu(NewMenu().Add(Item("Rebuild", func() {
		clicked = true
		tr.SetMenu(NewMenu().Add(Item("After", nil)))
		b.Refresh(tr)
	})))
	b.rebuild()

	d := &dbusMenu{b: b}
	if derr := d.Event(1, "clicked", dbus.MakeVariant(""), 0); derr != nil {
		t.Fatalf("Event: %v", derr)
	}
	if !clicked {
		t.Fatal("the click never reached the item")
	}
	_, layout, _ := d.GetLayout(0, -1, nil)
	if len(layout.Children) != 1 {
		t.Fatalf("root has %d children, want the rebuilt menu's 1", len(layout.Children))
	}
	if got := layout.Children[0].Value().(menuLayout).Props["label"].Value(); got != "After" {
		t.Errorf("the rebuilt row says %v, want After", got)
	}
}

// TestLiveAttachExportsTheItemAndQuitWithdrawsIt runs against a REAL session
// bus, and skips without one. CI gives it one with dbus-run-session.
//
// Attach is what go-widgets/application calls for Spec.Tray, and on Linux it
// used to be missing: Tray.Attach answered ErrNoBackend, application swallowed
// that, and the tray simply never appeared (go-widgets/application#28). So the
// assertion is the one a desktop shell makes -- the item's name is on the bus
// and its menu answers over it -- and then that Quit takes both away.
func TestLiveAttachExportsTheItemAndQuitWithdrawsIt(t *testing.T) {
	probe, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Skipf("no session bus: %v", err)
	}
	defer probe.Close()

	tr := New(nil).SetTooltip("live").SetMenu(NewMenu().Add(Item("Open", nil), Item("Quit", nil)))
	if _, ok := tr.backend.(*linuxBackend); !ok {
		t.Fatalf("default backend is %T, want *linuxBackend", tr.backend)
	}
	if err := tr.Attach(); err != nil {
		t.Fatalf("Attach = %v, want the item exported", err)
	}
	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())

	var owned bool
	if err := probe.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&owned); err != nil || !owned {
		t.Fatalf("after Attach, %s owned = %v (%v), want true", name, owned, err)
	}
	var rev uint32
	var layout menuLayout
	call := probe.Object(name, menuPath).Call(menuIface+".GetLayout", 0, int32(0), int32(-1), []string{})
	if err := call.Store(&rev, &layout); err != nil {
		t.Fatalf("GetLayout over the bus: %v", err)
	}
	if len(layout.Children) != 2 {
		t.Errorf("the menu over the bus has %d rows, want 2", len(layout.Children))
	}

	tr.Quit()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := probe.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&owned); err == nil && !owned {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("%s is still owned 5s after Quit: the item stayed on the bus", name)
}

// TestQuitBeforeAttachIsNotForgotten: application attaches from a goroutine and
// quits when its window returns, so a window closed at once can Quit first.
// The item must then not come up and stay.
func TestQuitBeforeAttachIsNotForgotten(t *testing.T) {
	b := &linuxBackend{}
	b.Quit()
	select {
	case <-b.newDone():
	default:
		t.Fatal("a Quit that came before the channel existed was forgotten")
	}
}
