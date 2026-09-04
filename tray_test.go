package tray

import (
	"testing"
	"time"
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
	if !tr.Visible() {
		t.Fatal("a fresh Tray must start visible")
	}
	// mutations before Run refresh the backend
	tr.SetTooltip("hi").SetIcon([]byte("PNG2")).SetMenu(NewMenu().Add(Item("Q", nil))).SetTitle("6%").SetVisible(false)
	if h.Refreshes != 5 {
		t.Errorf("refreshes = %d", h.Refreshes)
	}
	if h.LastVisible {
		t.Error("SetVisible(false) did not reach the backend")
	}
	if tr.Visible() {
		t.Error("Visible() did not reflect SetVisible(false)")
	}
	tr.SetVisible(true)
	// OnReady fires during Run; quitting from it lets Run return
	tr.OnReady(func() { ready = true; tr.Quit() })
	if err := tr.Run(); err != nil {
		t.Fatal(err)
	}
	if !ready || !h.Started {
		t.Error("tray did not start / ready")
	}
	if string(h.LastIcon) != "PNG2" || h.LastTitle != "6%" || h.LastTip != "hi" || h.LastMenu.Items[0].Label != "Q" {
		t.Errorf("snapshot = %q %q %q %v", h.LastIcon, h.LastTitle, h.LastTip, h.LastMenu)
	}
	// accessors
	if string(tr.Icon()) != "PNG2" || tr.Title() != "6%" || tr.Tooltip() != "hi" || tr.Menu() == nil {
		t.Error("accessors")
	}
	// Quit is idempotent
	tr.Quit()
}

func (f *fakeAttachBackend) Attach(t *Tray) error {
	f.attached++
	t.ready()
	return nil
}

func TestTrayAttach(t *testing.T) {
	// nil backend → ErrNoBackend.
	if err := (New([]byte("PNG")).WithBackend(nil)).Attach(); err != ErrNoBackend {
		t.Fatalf("nil-backend Attach = %v, want ErrNoBackend", err)
	}
	// backend without attach support (Headless) → ErrNoBackend.
	if err := New([]byte("PNG")).WithBackend(NewHeadless()).Attach(); err != ErrNoBackend {
		t.Fatalf("non-attacher Attach = %v, want ErrNoBackend", err)
	}
	// backend that supports attach → delegates, no error, fires OnReady.
	fb := &fakeAttachBackend{Headless: *NewHeadless()}
	ready := false
	tr := New([]byte("PNG")).WithBackend(fb).OnReady(func() { ready = true })
	if err := tr.Attach(); err != nil {
		t.Fatalf("attacher Attach = %v, want nil", err)
	}
	if fb.attached != 1 || !ready {
		t.Fatalf("attach not delegated: attached=%d ready=%v", fb.attached, ready)
	}
}

type fakeAttachBackend struct {
	Headless
	attached int
}

func TestTrayClose(t *testing.T) {
	// backend without removal support (Headless) → Close falls back to Quit.
	h := NewHeadless()
	tr := New([]byte("PNG")).WithBackend(h)
	done := make(chan error, 1)
	go func() { done <- tr.Run() }()
	tr.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after Close (fallback to Quit) = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not fall back to Quit: Run never returned")
	}

	// backend with removal support → delegates to Remove, does NOT touch Quit.
	fb := &fakeRemoverBackend{Headless: *NewHeadless()}
	tr2 := New([]byte("PNG")).WithBackend(fb)
	tr2.Close()
	if fb.removed != 1 {
		t.Fatalf("removed = %d, want 1", fb.removed)
	}
	if fb.closed {
		t.Fatal("Close on a remover backend must not fall back to Quit")
	}

	// nil backend → no panic.
	New([]byte("PNG")).WithBackend(nil).Close()
}

type fakeRemoverBackend struct {
	Headless
	removed int
}

func (f *fakeRemoverBackend) Remove(*Tray) { f.removed++ }
