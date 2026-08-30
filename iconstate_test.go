// Copyright (c) 2026, the go-widgets authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package tray

import (
	"sync"
	"testing"
	"time"

	"github.com/go-widgets/mvvm"
)

// The clock is driven by the test, not by the wall. A test that sleeps to let
// an animation advance is a test that fails on a loaded machine and teaches
// everyone to rerun it, which is worse than no test.
type clockLog struct {
	mu      sync.Mutex
	periods []time.Duration
	stops   int
}

func (c *clockLog) read() ([]time.Duration, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.periods...), c.stops
}

// The factory is called ON THE ANIMATOR'S GOROUTINE, so what it records is
// shared state like any other and takes the lock.
func fakeClock(t *testing.T) (beat chan time.Time, log *clockLog) {
	t.Helper()
	beat = make(chan time.Time, 16)
	log = &clockLog{}
	prev := tickerFor
	tickerFor = func(d time.Duration) (<-chan time.Time, func()) {
		log.mu.Lock()
		log.periods = append(log.periods, d)
		log.mu.Unlock()
		return beat, func() { log.mu.Lock(); log.stops++; log.mu.Unlock() }
	}
	t.Cleanup(func() { tickerFor = prev })
	return beat, log
}

// waitIcon waits for the tray's icon to become want, so the test synchronises
// on the value it cares about instead of on a duration.
func waitIcon(t *testing.T, h *Headless, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if icon, _, _ := h.Snapshot(); string(icon) == want {
			return
		}
		select {
		case <-deadline:
			icon, _, _ := h.Snapshot()
			t.Fatalf("icon is %q, want %q", icon, want)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestBindIconFollowsTheState(t *testing.T) {
	beat, clock := fakeClock(t)
	h := NewHeadless()
	tr := New(nil).WithBackend(h)

	state := mvvm.NewObservable("idle")
	icons := Icons[string]{
		"idle":    {[]byte("IDLE")},
		"paused":  {[]byte("PAUSED")},
		"running": {[]byte("R0"), []byte("R1"), []byte("R2")},
	}

	stop := BindIcon(tr, state, icons, 300*time.Millisecond)
	defer stop()

	// The initial state is drawn without waiting for a change: a tray that
	// stays blank until something happens is a tray that looks broken.
	waitIcon(t, h, "IDLE")

	state.Set("running")
	waitIcon(t, h, "R0")

	// Three frames over 300ms is a beat every 100ms — asserted, because a
	// period divided by the wrong thing animates at the wrong speed and
	// nothing else would notice.
	if ps, _ := clock.read(); len(ps) != 1 || ps[0] != 100*time.Millisecond {
		t.Errorf("ticker periods = %v, want one of 100ms", ps)
	}

	beat <- time.Now()
	waitIcon(t, h, "R1")
	beat <- time.Now()
	waitIcon(t, h, "R2")
	beat <- time.Now()
	waitIcon(t, h, "R0") // wraps

	// A still state must stop the clock rather than keep a ticker running for
	// an icon that never changes.
	state.Set("paused")
	waitIcon(t, h, "PAUSED")
	if _, stops := clock.read(); stops != 1 {
		t.Errorf("ticker stops = %d, want 1: the animation kept ticking for a still icon", stops)
	}
}

func TestBindIconLeavesAnUnmappedStateAlone(t *testing.T) {
	_, _ = fakeClock(t)
	h := NewHeadless()
	tr := New(nil).WithBackend(h)

	state := mvvm.NewObservable("idle")
	stop := BindIcon(tr, state, Icons[string]{"idle": {[]byte("IDLE")}}, time.Second)
	defer stop()
	waitIcon(t, h, "IDLE")

	// The caller's map is incomplete; the tray is not. Blanking here would look
	// like a broken tray rather than a missing entry.
	state.Set("nobody-mapped-this")
	time.Sleep(20 * time.Millisecond)
	if icon, _, _ := h.Snapshot(); string(icon) != "IDLE" {
		t.Errorf("icon = %q after an unmapped state, want it left alone", icon)
	}
}

func TestBindIconStopIsIdempotentAndEndsTheAnimation(t *testing.T) {
	beat, clock := fakeClock(t)
	h := NewHeadless()
	tr := New(nil).WithBackend(h)

	state := mvvm.NewObservable("running")
	stop := BindIcon(tr, state, Icons[string]{"running": {[]byte("A"), []byte("B")}}, time.Second)
	waitIcon(t, h, "A")

	stop()
	stop() // twice: a caller that stops in a defer AND on a path is normal

	// Nothing draws after stop. The beat is buffered, so this would be picked
	// up by a goroutine that was still running.
	beat <- time.Now()
	time.Sleep(20 * time.Millisecond)
	icon, _, _ := h.Snapshot()
	if string(icon) != "A" {
		t.Errorf("icon = %q after stop, want the animation to have ended", icon)
	}
	if _, stops := clock.read(); stops != 1 {
		t.Errorf("ticker stops = %d, want 1", stops)
	}
}

// show must never block, because it runs on whatever goroutine set the state.
func TestShowDropsStatesNobodyCouldHaveSeen(t *testing.T) {
	a := &iconAnimator{change: make(chan [][]byte, 1), done: make(chan struct{})}
	a.show(nil) // no frames: nothing to draw, and no panic
	for i := 0; i < 50; i++ {
		a.show([][]byte{[]byte("x")})
	}
	if len(a.change) != 1 {
		t.Errorf("pending states = %d, want 1: only the newest state matters", len(a.change))
	}
}
