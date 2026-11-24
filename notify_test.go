package tray

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf16"
)

func TestNotificationValidate(t *testing.T) {
	cases := []struct {
		name string
		n    Notification
		want error // nil = valid; errAny = some error
	}{
		{"ok", Notification{Title: "Tunnel dropped", Body: "Reconnecting"}, nil},
		{"title only", Notification{Title: "x"}, nil},
		{"empty title", Notification{Body: "b"}, ErrEmptyTitle},
		{"blank title", Notification{Title: " \t"}, ErrEmptyTitle},
		{"title at bound", Notification{Title: strings.Repeat("é", MaxNotificationTitle)}, nil},
		{"title over bound", Notification{Title: strings.Repeat("a", MaxNotificationTitle+1)}, errAny},
		{"body at bound", Notification{Title: "t", Body: strings.Repeat("a", MaxNotificationBody)}, nil},
		{"body over bound", Notification{Title: "t", Body: strings.Repeat("a", MaxNotificationBody+1)}, errAny},
		{"newline and tab allowed", Notification{Title: "t", Body: "a\nb\tc"}, nil},
		{"NUL refused", Notification{Title: "t", Body: "a\x00b"}, errAny},
		{"escape refused", Notification{Title: "t\x1b[31m"}, errAny},
		{"DEL refused", Notification{Title: "t\x7f"}, errAny},
		{"invalid UTF-8 refused", Notification{Title: "t", Body: "a\xffb"}, errAny},
	}
	for _, c := range cases {
		err := c.n.Validate()
		switch {
		case c.want == nil && err != nil:
			t.Errorf("%s: unexpected %v", c.name, err)
		case c.want == errAny && err == nil:
			t.Errorf("%s: accepted", c.name)
		case c.want != nil && c.want != errAny && !errors.Is(err, c.want):
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

var errAny = errors.New("any error")

func TestTrayNotifyHeadless(t *testing.T) {
	h := NewHeadless()
	tr := New(nil).WithBackend(h)

	if err := tr.Notify(Notification{Title: "t"}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("before Run: err = %v, want ErrNotRunning", err)
	}
	ready := make(chan struct{})
	tr.OnReady(func() { close(ready) })
	go tr.Run()
	<-ready
	defer tr.Quit()

	var clicked atomic.Bool
	n := Notification{Title: "Tunnel dropped", Body: "b", OnClick: func() { clicked.Store(true) }}
	if err := tr.Notify(n); err != nil {
		t.Fatal(err)
	}
	if len(h.Notified) != 1 || h.Notified[0].Title != "Tunnel dropped" {
		t.Fatalf("recorded %+v", h.Notified)
	}
	h.Notified[0].OnClick()
	if !clicked.Load() {
		t.Fatal("the recorded callback is not the caller's")
	}
	// An invalid notification never reaches the backend.
	if err := tr.Notify(Notification{}); !errors.Is(err, ErrEmptyTitle) || len(h.Notified) != 1 {
		t.Fatalf("invalid: err = %v, recorded %d", err, len(h.Notified))
	}
}

// bareBackend implements Backend and nothing else.
type bareBackend struct{}

func (bareBackend) Run(*Tray) error { return nil }
func (bareBackend) Refresh(*Tray)   {}
func (bareBackend) Quit()           {}

// noPanic runs f and reports a panic as a failed assertion. A broken guard then
// fails the test that covers it, instead of killing the test binary and hiding
// every test that would have run after it.
func noPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

func TestTrayNotifyWithoutCapability(t *testing.T) {
	var err error
	noPanic(t, "Notify on a backend that cannot notify", func() {
		err = New(nil).WithBackend(bareBackend{}).Notify(Notification{Title: "t"})
	})
	if !errors.Is(err, ErrNoNotifier) {
		t.Fatalf("err = %v, want ErrNoNotifier", err)
	}
	tr := New(nil)
	tr.backend = nil
	if err := tr.Notify(Notification{Title: "t"}); !errors.Is(err, ErrNoBackend) {
		t.Fatalf("err = %v, want ErrNoBackend", err)
	}
}

func TestHeadlessNotifyAfterQuit(t *testing.T) {
	h := NewHeadless()
	h.Started = true
	h.Quit()
	if err := h.Notify(nil, Notification{Title: "t"}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

func TestUTF16TruncNeverSplitsAPair(t *testing.T) {
	// 62 BMP units then an astral rune (a surrogate pair): cutting at 63 would keep
	// only the high half.
	s := strings.Repeat("a", 62) + "\U0001F600"
	units := utf16.Encode([]rune(s))
	got := utf16Trunc(units, 63)
	if len(got) != 62 {
		t.Fatalf("len = %d, want 62 (the pair dropped whole)", len(got))
	}
	if r := utf16.Decode(got); string(r) != strings.Repeat("a", 62) {
		t.Fatalf("decoded %q", string(r))
	}
	var short []uint16
	noPanic(t, "utf16Trunc of an input shorter than max", func() { short = utf16Trunc(units[:5], 63) })
	if len(short) != 5 {
		t.Fatalf("short input changed: %d", len(short))
	}
	// An input of exactly max units fits as it is, even when its last unit is a
	// high surrogate: nothing was cut, so no pair was split.
	exact := append(utf16.Encode([]rune(strings.Repeat("a", 62))), 0xD83D)
	var same []uint16
	noPanic(t, "utf16Trunc of an input of exactly max units", func() { same = utf16Trunc(exact, 63) })
	if len(same) != 63 {
		t.Fatalf("input of exactly max units cut to %d, want 63", len(same))
	}
	if got := utf16Trunc(units, 0); len(got) != 0 {
		t.Fatalf("max 0 kept %d units", len(got))
	}
}

func TestBalloonUnitsFitTheBuffers(t *testing.T) {
	// 63 astral runes pass Validate (63 runes) and are 126 units: must be cut to 63.
	n := Notification{Title: strings.Repeat("\U0001F600", MaxNotificationTitle), Body: strings.Repeat("\U0001F600", MaxNotificationBody)}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	title, info := balloonUnits(n)
	if len(title) > 63 || len(info) > 255 {
		t.Fatalf("title %d, info %d units", len(title), len(info))
	}
	for _, u := range [][]uint16{title, info} {
		if len(u) > 0 && utf16.IsSurrogate(rune(u[len(u)-1])) && u[len(u)-1] < 0xDC00 {
			t.Fatal("ends on a lone high surrogate")
		}
	}
}

func TestClickTable(t *testing.T) {
	c := newClickTable[int]()
	var ran int
	c.add(1, func() { ran++ })
	c.add(1, func() { ran += 10 }) // same id replaces, does not duplicate
	if fn := c.take(1); fn == nil {
		t.Fatal("missing")
	} else {
		fn()
	}
	if ran != 10 {
		t.Fatalf("ran = %d, want the replacement (10)", ran)
	}
	if c.take(1) != nil {
		t.Fatal("take must remove")
	}
	for i := 0; i < maxPendingClicks+5; i++ {
		c.add(100+i, func() {})
	}
	if len(c.fns) != maxPendingClicks || len(c.order) != maxPendingClicks {
		t.Fatalf("table grew to %d/%d", len(c.fns), len(c.order))
	}
	if c.take(100) != nil {
		t.Fatal("the oldest entry should have been evicted")
	}
	if c.take(100+maxPendingClicks+4) == nil {
		t.Fatal("the newest entry should remain")
	}
}

func FuzzNotificationBounds(f *testing.F) {
	f.Add("Tunnel dropped", "body")
	f.Add("", "")
	f.Add("\x00", "\xff\xfe")
	f.Add("\U0001F600", strings.Repeat("\U0001F600", 300))
	f.Fuzz(func(t *testing.T, title, body string) {
		n := Notification{Title: title, Body: body}
		err := n.Validate() // must never panic
		tu, iu := balloonUnits(n)
		if len(tu) > 63 || len(iu) > 255 {
			t.Fatalf("balloon buffers overrun: %d/%d", len(tu), len(iu))
		}
		if err == nil && (strings.ContainsRune(title, 0) || strings.ContainsRune(body, 0)) {
			t.Fatal("a NUL passed validation")
		}
	})
}
