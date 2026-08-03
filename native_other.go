//go:build tray_native && !darwin && !windows && !linux

package tray

// defaultBackend for the tray_native build on platforms with no native backend
// (darwin, windows and linux each have their own). Returns nil so Run reports
// ErrNoBackend rather than linking a non-existent backend.
func defaultBackend() Backend { return nil }
