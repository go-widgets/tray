//go:build tray_native && darwin

package tray

import "testing"

// The mirror of defaultbackend_absent_test.go: with the native backend linked
// in, a tray built without an explicit backend must find one.
//
// Run is deliberately NOT called here. With the native backend it enters the
// application's run loop and does not come back, and in a test binary that loop
// is asked for from a goroutine rather than the process main thread, which is a
// SIGTRAP rather than a failure. What Run does once it has a backend is covered
// by the live suite through Attach, which places a real item without taking the
// run loop.
func TestDefaultBackendIsThereWhenLinkedIn(t *testing.T) {
	b := defaultBackend()
	if b == nil {
		t.Fatal("defaultBackend() is nil in a tray_native build: the backend was not linked in")
	}
	if _, ok := b.(*darwinBackend); !ok {
		t.Errorf("defaultBackend() is %T, want *darwinBackend", b)
	}
	if tr := New(nil); tr.backend == nil {
		t.Error("New picked up no backend, so Run would answer ErrNoBackend in a build that has one")
	}
}
