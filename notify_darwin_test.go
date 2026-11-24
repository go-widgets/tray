package tray

import (
	"errors"
	"sync/atomic"
	"testing"
)

type fakeCenter struct {
	notBundled bool
	authErr    error
	postErr    error
	auths      int
	posted     []string
}

func (f *fakeCenter) bundled() bool { return !f.notBundled }
func (f *fakeCenter) authorize() error {
	f.auths++
	return f.authErr
}
func (f *fakeCenter) post(id, title, body string) error {
	if f.postErr != nil {
		return f.postErr
	}
	f.posted = append(f.posted, id+"|"+title+"|"+body)
	return nil
}

func resetAuth() { authMu.Lock(); authGranted = false; authMu.Unlock() }

func TestPostDarwinRefusesWithoutBundle(t *testing.T) {
	resetAuth()
	f := &fakeCenter{notBundled: true}
	if err := postDarwin(f, newClickTable[string](), Notification{Title: "t"}); !errors.Is(err, ErrNotBundled) {
		t.Fatalf("err = %v", err)
	}
	if f.auths != 0 {
		t.Fatal("asked for authorization from a bare executable")
	}
}

func TestPostDarwinAuthorizationIsAskedOnceAndDenialIsNotCached(t *testing.T) {
	resetAuth()
	f := &fakeCenter{authErr: ErrNotAuthorized}
	clicks := newClickTable[string]()
	if err := postDarwin(f, clicks, Notification{Title: "t"}); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v", err)
	}
	if err := postDarwin(f, clicks, Notification{Title: "t"}); !errors.Is(err, ErrNotAuthorized) || f.auths != 2 {
		t.Fatalf("a denial must be re-asked: auths = %d, err = %v", f.auths, err)
	}
	f.authErr = nil
	for i := 0; i < 3; i++ {
		if err := postDarwin(f, clicks, Notification{Title: "t", Body: "b"}); err != nil {
			t.Fatal(err)
		}
	}
	if f.auths != 3 || len(f.posted) != 3 {
		t.Fatalf("a grant is cached: auths = %d, posted = %d", f.auths, len(f.posted))
	}
	resetAuth()
}

func TestPostDarwinClickRegistration(t *testing.T) {
	resetAuth()
	f := &fakeCenter{}
	clicks := newClickTable[string]()
	var n atomic.Int32
	if err := postDarwin(f, clicks, Notification{Title: "t", OnClick: func() { n.Add(1) }}); err != nil {
		t.Fatal(err)
	}
	if len(clicks.fns) != 1 {
		t.Fatalf("callbacks = %d", len(clicks.fns))
	}
	// A failed post leaves nothing behind.
	f.postErr = errors.New("boom")
	before := len(clicks.fns)
	if err := postDarwin(f, clicks, Notification{Title: "t", OnClick: func() {}}); err == nil || len(clicks.fns) != before {
		t.Fatalf("err = %v, callbacks %d -> %d", err, before, len(clicks.fns))
	}
	// No OnClick, no entry.
	f.postErr = nil
	postDarwin(f, clicks, Notification{Title: "t"})
	if len(clicks.fns) != before {
		t.Fatal("a notification without OnClick registered a callback")
	}
	resetAuth()
}

// The REAL center, on this machine: a test binary is not an app bundle, so the
// call must come back with ErrNotBundled instead of Apple's exception killing
// the process. This is the check the bundle requirement rests on.
func TestRealCenterRefusesABareExecutable(t *testing.T) {
	resetAuth()
	err := postDarwin(&realCenter{}, newClickTable[string](), Notification{Title: "t"})
	if !errors.Is(err, ErrNotBundled) {
		t.Fatalf("err = %v, want ErrNotBundled", err)
	}
}
