package tray

import (
	"errors"
	"testing"

	"github.com/go-macos/objc"
)

// The real center cannot run without an app bundle and the user's consent, so
// the decisions around its Objective-C calls are asserted through the seams.

func TestNotificationIDFailureStopsThePost(t *testing.T) {
	boom := errors.New("no entropy")
	saved := randRead
	defer func() { randRead = saved }()
	randRead = func([]byte) (int, error) { return 0, boom }
	resetAuth()
	defer resetAuth()

	if _, err := newNotificationID(); !errors.Is(err, boom) {
		t.Fatalf("newNotificationID = %v, want the random source's error", err)
	}
	c := &fakeCenter{}
	clicks := newClickTable[string]()
	err := postDarwin(c, clicks, Notification{Title: "t", OnClick: func() {}})
	if !errors.Is(err, boom) {
		t.Fatalf("postDarwin = %v, want the random source's error", err)
	}
	if len(c.posted) != 0 || len(clicks.fns) != 0 {
		t.Fatalf("nothing may be posted or registered without an id: %v %d", c.posted, len(clicks.fns))
	}
}

func TestBundledNeedsAppKitAndABundleIdentifier(t *testing.T) {
	savedKit, savedID := appKitReady, mainBundleID
	defer func() { appKitReady, mainBundleID = savedKit, savedID }()
	r := &realCenter{}
	for _, c := range []struct {
		kit  error
		id   string
		want bool
	}{
		{nil, "com.example.app", true},
		{nil, "", false},
		{errors.New("no appkit"), "com.example.app", false},
	} {
		appKitReady = func() error { return c.kit }
		mainBundleID = func() string { return c.id }
		if got := r.bundled(); got != c.want {
			t.Errorf("kit=%v id=%q: bundled = %v, want %v", c.kit, c.id, got, c.want)
		}
	}
}

// swapSetupSeams replaces the three Objective-C steps of setup and returns the
// restore.
func swapSetupSeams(load func() error, center func() objc.ID, reg func() (objc.ID, error)) func() {
	l, c, g := unLoad, unCurrentCenter, unRegisterDelegate
	unLoad, unCurrentCenter, unRegisterDelegate = load, center, reg
	return func() { unLoad, unCurrentCenter, unRegisterDelegate = l, c, g }
}

func TestSetupReportsEachStepThatFails(t *testing.T) {
	boom := errors.New("boom")
	ok := func() error { return nil }
	someCenter := func() objc.ID { return 1 }
	noReg := func() (objc.ID, error) { return 0, boom }

	t.Run("framework will not load", func(t *testing.T) {
		defer swapSetupSeams(func() error { return boom }, someCenter, noReg)()
		if err := (&realCenter{}).setup(); !errors.Is(err, boom) {
			t.Fatalf("setup = %v, want the load error", err)
		}
	})
	t.Run("no center", func(t *testing.T) {
		defer swapSetupSeams(ok, func() objc.ID { return 0 }, noReg)()
		if err := (&realCenter{}).setup(); !errors.Is(err, ErrNoNotifier) {
			t.Fatalf("setup = %v, want ErrNoNotifier", err)
		}
	})
	t.Run("delegate class will not register", func(t *testing.T) {
		defer swapSetupSeams(ok, someCenter, noReg)()
		if err := (&realCenter{}).setup(); !errors.Is(err, ErrNoTargetClass) {
			t.Fatalf("setup = %v, want ErrNoTargetClass", err)
		}
	})
}

func TestAuthorizeAndPostReturnTheSetupError(t *testing.T) {
	boom := errors.New("setup failed")
	r := &realCenter{}
	r.once.Do(func() {})
	r.err = boom
	if err := r.authorize(); !errors.Is(err, boom) {
		t.Errorf("authorize = %v, want the setup error", err)
	}
	if err := r.post("id", "t", "b"); !errors.Is(err, boom) {
		t.Errorf("post = %v, want the setup error", err)
	}
}

// Registering the delegate needs no bundle and no center, only the runtime; the
// class name is global to the process, so the second registration is refused.
func TestRegisterDelegateRefusesASecondRegistration(t *testing.T) {
	first, err := registerDelegate()
	if err != nil || first == 0 {
		t.Fatalf("first registration = %v, %v", first, err)
	}
	if id, err := registerDelegate(); err == nil {
		t.Fatalf("a second registration of the same class returned %v, want an error", id)
	}
}

func TestBareExecutableHasNoBundleIdentifier(t *testing.T) {
	if err := ensureAppKit(); err != nil {
		t.Skip(err)
	}
	if id := mainBundleID(); id != "" {
		t.Fatalf("a test binary reported bundle id %q", id)
	}
}

func TestAuthAnswerError(t *testing.T) {
	if err := (authAnswer{granted: true}).err(); err != nil {
		t.Errorf("granted: %v", err)
	}
	if err := (authAnswer{granted: false}).err(); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("denied: %v", err)
	}
	// A failure is reported with the system's text even when "granted" is true.
	err := (authAnswer{granted: true, failed: true, text: "no way"}).err()
	if !errors.Is(err, ErrNotAuthorized) || err.Error() == ErrNotAuthorized.Error() {
		t.Errorf("failed: %v must wrap ErrNotAuthorized and carry the text", err)
	}
}

func TestPostAnswer(t *testing.T) {
	text := func(objc.ID) string { return "denied by policy" }
	if got := postAnswer(0, text); got != "" {
		t.Errorf("no error object = %q, want accepted", got)
	}
	if got := postAnswer(7, text); got != "refused: denied by policy" {
		t.Errorf("error object = %q", got)
	}
}
