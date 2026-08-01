package tray

import (
	"testing"
)

func TestMenuBuildersAndFind(t *testing.T) {
	clicks := 0
	sub := NewMenu().Add(Item("Sub A", func() { clicks++ }))
	m := NewMenu().Add(
		Item("Open", func() { clicks++ }),
		Separator(),
		SubMenu("More", sub),
	)
	if len(m.Items) != 3 {
		t.Fatalf("items = %d", len(m.Items))
	}
	if m.Items[1].Separator != true {
		t.Error("expected separator")
	}
	// Find descends into submenus
	if it := m.Find(2, 0); it == nil || it.Label != "Sub A" {
		t.Errorf("Find submenu = %v", it)
	}
	if m.Find(9) != nil || m.Find(0, 0) != nil || m.Find(-1) != nil {
		t.Error("invalid Find paths should be nil")
	}
}

func TestActivate(t *testing.T) {
	// plain item
	got := 0
	Item("x", func() { got++ }).Activate()
	if got != 1 {
		t.Errorf("plain activate = %d", got)
	}
	// nil OnClick is safe
	Item("x", nil).Activate()
	// separator / disabled / submenu are no-ops
	Separator().Activate()
	di := Item("d", func() { got++ })
	di.Disabled = true
	di.Activate()
	SubMenu("s", NewMenu()).Activate()
	if got != 1 {
		t.Errorf("no-op items ran callbacks: %d", got)
	}
	// checkbox toggles + reports state
	var state bool
	cb := Checkbox("c", false, func(v bool) { state = v })
	cb.Activate()
	if !cb.Checked || !state {
		t.Errorf("checkbox on = %v %v", cb.Checked, state)
	}
	cb.Activate()
	if cb.Checked || state {
		t.Errorf("checkbox off = %v %v", cb.Checked, state)
	}
	// checkbox with nil onToggle still flips
	cb2 := Checkbox("c2", false, nil)
	cb2.Activate()
	if !cb2.Checked {
		t.Error("nil-onToggle checkbox should still flip")
	}
}

func TestTrayLifecycle(t *testing.T) {
	h := NewHeadless()
	ready := false
	tr := New([]byte("PNG")).WithBackend(h).OnReady(func() { ready = true })
	// mutations before Run refresh the backend
	tr.SetTooltip("hi").SetIcon([]byte("PNG2")).SetMenu(NewMenu().Add(Item("Q", nil)))
	if h.Refreshes != 3 {
		t.Errorf("refreshes = %d", h.Refreshes)
	}
	// OnReady fires during Run; quitting from it lets Run return
	tr.OnReady(func() { ready = true; tr.Quit() })
	if err := tr.Run(); err != nil {
		t.Fatal(err)
	}
	if !ready || !h.Started {
		t.Error("tray did not start / ready")
	}
	if string(h.LastIcon) != "PNG2" || h.LastTip != "hi" || h.LastMenu.Items[0].Label != "Q" {
		t.Errorf("snapshot = %q %q %v", h.LastIcon, h.LastTip, h.LastMenu)
	}
	// accessors
	if string(tr.Icon()) != "PNG2" || tr.Tooltip() != "hi" || tr.Menu() == nil {
		t.Error("accessors")
	}
	// Quit is idempotent
	tr.Quit()
}

func TestRunNoBackend(t *testing.T) {
	tr := New(nil) // defaultBackend() is nil until a native backend exists
	if err := tr.Run(); err != ErrNoBackend {
		t.Errorf("Run without backend = %v", err)
	}
	// Quit without a backend is a safe no-op
	tr.Quit()
	tr.SetTooltip("x") // refresh without a backend is safe too
}

func TestDefaultBackendNil(t *testing.T) {
	if defaultBackend() != nil {
		t.Error("defaultBackend is nil until a native backend is added")
	}
}
