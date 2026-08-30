// Copyright (c) 2026, the go-widgets authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package tray

import (
	"sync"
	"time"

	"github.com/go-widgets/mvvm"
)

// Icons is what the menu bar shows for each state of whatever the tray is
// watching: one entry per state, and each entry is one or more PNG frames.
//
// A single frame is a still icon. Several frames are an animation, which is the
// point — "something is happening" is the one thing a menu bar can say without
// the user opening anything, and a still icon cannot say it.
type Icons[S comparable] map[S][][]byte

// tickerFor is the seam the animator gets its clock through. Tests replace it
// so the frames advance when they say so rather than when a wall clock does; a
// test that waits on real time is a test that fails on a loaded machine.
var tickerFor = func(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// BindIcon makes the tray's icon follow state.
//
// Whenever state changes the icon becomes that state's entry, animating through
// its frames over period when there is more than one. It is a package function
// rather than a method because a method cannot carry its own type parameter,
// and the state a caller watches is theirs to name — a string, an enum, a bool.
//
// A state with no entry leaves the icon alone rather than blanking it: a tray
// that goes blank on an unmapped state looks broken, and it is the caller's map
// that is incomplete, not the tray.
//
// The returned function stops the animation and unsubscribes. It is safe to
// call more than once, and it must be called: neither the goroutine nor the
// subscription ends on its own.
func BindIcon[S comparable](t *Tray, state *mvvm.Observable[S], icons Icons[S], period time.Duration) (stop func()) {
	a := &iconAnimator{tray: t, period: period, change: make(chan [][]byte, 1), done: make(chan struct{})}

	frames := func(s S) [][]byte { return icons[s] }
	unsubscribe := state.Subscribe(func(s S) { a.show(frames(s)) })
	a.show(frames(state.Get()))

	go a.run()

	var once sync.Once
	return func() {
		once.Do(func() {
			unsubscribe()
			close(a.done)
		})
	}
}

// iconAnimator holds the one goroutine that draws frames, so the tray is
// written to from a single place however many times the state changes.
type iconAnimator struct {
	tray   *Tray
	period time.Duration
	change chan [][]byte
	done   chan struct{}
}

// show hands the animator a new set of frames, replacing any it has not started
// yet. It never blocks: a state that changes faster than the animator reads is
// a state whose intermediate values nobody could have seen anyway.
func (a *iconAnimator) show(frames [][]byte) {
	if len(frames) == 0 {
		return
	}
	// One of the two always proceeds: the buffer holds one, so it is either
	// sendable or receivable. There is no third case to guard, and adding a
	// default here would be a branch no test could ever reach.
	for {
		select {
		case a.change <- frames:
			return
		case <-a.change: // drop the one waiting; only the newest state matters
		}
	}
}

// run draws. A still icon is drawn once and then costs nothing, which is what
// the menu bar spends most of its life doing.
func (a *iconAnimator) run() {
	var frames [][]byte
	var tick <-chan time.Time
	var stopTick func()
	i := 0

	defer func() {
		if stopTick != nil {
			stopTick()
		}
	}()

	for {
		select {
		case <-a.done:
			return
		case f := <-a.change:
			if stopTick != nil {
				stopTick()
				tick, stopTick = nil, nil
			}
			frames, i = f, 0
			a.tray.SetIcon(frames[0])
			if len(frames) > 1 {
				tick, stopTick = tickerFor(a.period / time.Duration(len(frames)))
			}
		case <-tick:
			i = (i + 1) % len(frames)
			a.tray.SetIcon(frames[i])
		}
	}
}
