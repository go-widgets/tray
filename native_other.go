//go:build !darwin && !windows && !linux

package tray

// defaultBackend on a platform with no native backend of its own
// (darwin, windows and linux each have their own). Returns nil so Run reports
// ErrNoBackend rather than linking a non-existent backend.
func defaultBackend() Backend { return nil }
