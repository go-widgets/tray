package tray

import "sync"

// Headless is a display-less Backend for tests and CI. It records the tray
// state applied to it and blocks Run until Quit, so a tray can be exercised
// end-to-end without a real desktop session.
type Headless struct {
	mu        sync.Mutex
	Started   bool
	Refreshes int
	LastIcon  []byte
	LastTip   string
	LastMenu  *Menu

	quit   chan struct{}
	closed bool
}

// NewHeadless returns a ready headless backend.
func NewHeadless() *Headless { return &Headless{quit: make(chan struct{})} }

// Run marks the tray started, snapshots its state, signals readiness and blocks
// until Quit.
func (h *Headless) Run(t *Tray) error {
	h.mu.Lock()
	h.Started = true
	h.snapshot(t)
	h.mu.Unlock()
	t.ready()
	<-h.quit
	return nil
}

// Refresh snapshots the tray's current state.
func (h *Headless) Refresh(t *Tray) {
	h.mu.Lock()
	h.Refreshes++
	h.snapshot(t)
	h.mu.Unlock()
}

// Quit unblocks Run (idempotent).
func (h *Headless) Quit() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.quit)
	}
}

func (h *Headless) snapshot(t *Tray) {
	h.LastIcon, h.LastTip, h.LastMenu = t.Icon(), t.Tooltip(), t.Menu()
}
