//go:build tray_native && !darwin

package tray

// defaultBackend for the tray_native build on platforms whose native backend is
// not yet implemented (windows Shell_NotifyIcon and linux StatusNotifierItem are
// the next increments). Returns nil so Run reports ErrNoBackend rather than
// linking a non-existent backend.
func defaultBackend() Backend { return nil }
