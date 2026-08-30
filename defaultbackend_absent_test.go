//go:build !tray_native

package tray

import "testing"

// These two assert what is true of a build WITHOUT the native backend linked
// in, which is why they are tagged.
//
// They used to sit untagged in tray_test.go, where `go test -tags tray_native`
// ran them against the real backend: New(nil) then picks up the darwin one,
// Run enters [NSApp run] on a goroutine that is not the process main thread,
// and the binary dies on SIGTRAP naming no test at all. The mirror assertions
// for the tagged build are in defaultbackend_present_test.go.

func TestRunNoBackend(t *testing.T) {
	tr := New(nil) // defaultBackend() is nil until a native backend exists
	if err := tr.Run(); err != ErrNoBackend {
		t.Errorf("Run without backend = %v", err)
	}
	// Quit without a backend is a safe no-op
	tr.Quit()
	tr.SetTooltip("x") // refresh without a backend is safe too
}

func TestDefaultBackendNil(t *testing.T) {
	if defaultBackend() != nil {
		t.Error("defaultBackend is nil until a native backend is added")
	}
}

// fakeAttachBackend is a Headless that also implements the optional attacher
// capability, to exercise Tray.Attach's "backend supports attach" branch.
