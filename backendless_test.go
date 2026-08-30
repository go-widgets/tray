// Copyright (c) 2026 the go-widgets authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package tray

import (
	"errors"
	"testing"
)

// TestATrayWithNoBackendSaysSoRatherThanDoingNothing.
//
// Every platform this package knows now has a backend by default, so a Tray
// made the ordinary way has one. A Tray without is still reachable -- a caller
// that passes nil to WithBackend, a platform nobody has written yet -- and the
// difference between "there is no tray here" and "your tray silently does
// nothing" is the whole of what a caller needs to be told.
func TestATrayWithNoBackendSaysSoRatherThanDoingNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		t    *Tray
	}{
		{"a tray built with nothing behind it", &Tray{menu: NewMenu()}},
		{"a tray given a nil backend on purpose", New(nil).WithBackend(nil)},
	} {
		if err := c.t.Run(); !errors.Is(err, ErrNoBackend) {
			t.Errorf("%s: Run = %v, want ErrNoBackend", c.name, err)
		}
		if err := c.t.Attach(); !errors.Is(err, ErrNoBackend) {
			t.Errorf("%s: Attach = %v, want ErrNoBackend", c.name, err)
		}
		// Quit asks nothing of a backend that is not there, and must not panic
		// on the way out of a program that never got one.
		c.t.Quit()
	}
}
