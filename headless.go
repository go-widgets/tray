package tray

import "sync"

// Headless is a display-less Backend for tests and CI. It records the tray
// state applied to it and blocks Run until Quit, so a tray can be exercised
// end-to-end without a real desktop session.
type Headless struct {
	mu          sync.Mutex
	Started     bool
	Refreshes   int
	LastIcon    []byte
	LastTitle   string
	LastTip     string
	LastMenu    *Menu
	LastVisible bool

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

// Snapshot returns the state this backend last recorded, under the lock.
//
// Reading the fields directly is safe only while nothing can refresh
// concurrently. That used to be every caller; an icon bound to an observable
// state refreshes from the animator's own goroutine, so a reader that wants to
// watch it happen needs this.
func (h *Headless) Snapshot() (icon []byte, tip string, menu *Menu) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.LastIcon, h.LastTip, h.LastMenu
}

func (h *Headless) snapshot(t *Tray) {
	h.LastIcon, h.LastTitle, h.LastTip, h.LastMenu = t.Icon(), t.Title(), t.Tooltip(), t.Menu()
	h.LastVisible = t.Visible()
}
