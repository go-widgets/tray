// Copyright (c) 2026 the go-widgets/tray authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package tray

import (
	"sync"
	"testing"
	"time"
)

// TestWhatTheTrayShowsCanBeSetWhileTheLoopReadsIt is a race regression.
//
// The platform loop reads the icon, the tooltip and the menu to draw them, and
// whoever changes them is somewhere else: BindIcon writes the icon from a
// ticker of its own, and an application changes a menu when its state changes.
// Those were plain fields, and the race detector caught it the moment an icon
// was bound to application state -- SetIcon writing while the loop's Refresh
// read.
//
// It fails only under -race, which is exactly why it exists: without the
// detector the unlocked version passes, and with it the unlocked version fails
// here every time.
func TestWhatTheTrayShowsCanBeSetWhileTheLoopReadsIt(t *testing.T) {
	h := NewHeadless()
	ready := make(chan struct{})
	tr := New([]byte("first")).WithBackend(h).SetTooltip("first").
		OnReady(func() { close(ready) })
	done := make(chan error, 1)
	go func() { done <- tr.Run() }()

	// Wait until the loop is actually reading, or the test would race nothing.
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop never started")
	}

	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range 50 {
				tr.SetIcon([]byte{byte(i), byte(n)})
				tr.SetTooltip("changed")
				tr.SetMenu(NewMenu())
			}
		}()
	}
	// And read from a third party at the same time, the way a backend does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			_, _, _ = tr.Icon(), tr.Tooltip(), tr.Menu()
		}
	}()
	wg.Wait()

	tr.Quit()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
	if len(tr.Icon()) == 0 {
		t.Error("the tray ended with no icon at all")
	}
}
