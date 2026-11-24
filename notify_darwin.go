package tray

// macOS notifications: UNUserNotificationCenter (UserNotifications.framework),
// through the same purego Objective-C bridge the status item uses. No CGO.
//
// ⚠ REQUIREMENTS, which are Apple's and not ours:
//
//   - The process must run from an app BUNDLE (Foo.app with an Info.plist that
//     has CFBundleIdentifier). For a bare executable -- `go run`, `go test`, a
//     binary in /usr/local/bin -- +currentNotificationCenter raises an
//     Objective-C exception and kills the process; Notify checks the main bundle
//     first and returns [ErrNotBundled] instead.
//   - The user must allow notifications for the app. The first Notify asks the
//     system for authorization (alert + sound), which shows the system prompt
//     once and waits for the answer; a refusal is [ErrNotAuthorized] and is not
//     cached, so enabling it in System Settings > Notifications takes effect on
//     the next call.
//   - Clicks reach OnClick through a delegate registered on the center; an app
//     that sets its own delegate afterwards replaces it.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-macos/objc"
)

const (
	userNotificationsPath = "/System/Library/Frameworks/UserNotifications.framework/UserNotifications"

	// UNAuthorizationOptionSound | UNAuthorizationOptionAlert.
	unAuthSoundAlert = 1<<1 | 1<<2
	// UNNotificationPresentationOptionSound | ...List | ...Banner: show the
	// notification even while the app is frontmost (the default is to hide it).
	unPresentAll = 1<<1 | 1<<3 | 1<<4

	// authTimeout is how long Notify waits for the user to answer the system's
	// one-time prompt.
	authTimeout = 2 * time.Minute
)

// unCenter is the slice of UserNotifications the flow needs; tests replace it.
type unCenter interface {
	bundled() bool
	// authorize blocks until the system has answered; nil means granted.
	authorize() error
	post(id, title, body string) error
}

var (
	unImpl   unCenter = &realCenter{}
	unClicks          = newClickTable[string]()

	authMu      sync.Mutex
	authGranted bool
)

// Notify implements [Notifier]. The tray need not be running on macOS.
func (b *darwinBackend) Notify(_ *Tray, n Notification) error {
	return postDarwin(unImpl, unClicks, n)
}

// postDarwin is the platform-neutral half of Notify, over any unCenter.
func postDarwin(c unCenter, clicks *clickTable[string], n Notification) error {
	if !c.bundled() {
		return ErrNotBundled
	}
	authMu.Lock()
	granted := authGranted
	authMu.Unlock()
	if !granted {
		if err := c.authorize(); err != nil {
			return err
		}
		authMu.Lock()
		authGranted = true
		authMu.Unlock()
	}
	id, err := newNotificationID()
	if err != nil {
		return err
	}
	if n.OnClick != nil {
		clicks.add(id, n.OnClick)
	}
	if err := c.post(id, n.Title, n.Body); err != nil {
		clicks.take(id)
		return err
	}
	return nil
}

// randRead is a variable so a test can make the system's random source fail.
var randRead = rand.Read

func newNotificationID() (string, error) {
	var raw [16]byte
	if _, err := randRead(raw[:]); err != nil {
		return "", err
	}
	return "go-widgets-tray-" + hex.EncodeToString(raw[:]), nil
}

// realCenter talks to the live UNUserNotificationCenter.
type realCenter struct {
	once   sync.Once
	center objc.ID
	err    error
}

// The calls into Objective-C that need a bundle, an app or the user, behind
// variables: the decisions made around them are what the tests assert.
var (
	appKitReady  = ensureAppKit
	mainBundleID = func() string {
		bid := objc.ClassID("NSBundle").Send(objc.Sel("mainBundle")).Send(objc.Sel("bundleIdentifier"))
		if bid == 0 {
			return ""
		}
		return objc.GoString(bid)
	}
	unLoad          = func() error { return objc.Load(userNotificationsPath) }
	unCurrentCenter = func() objc.ID {
		return objc.ClassID("UNUserNotificationCenter").Send(objc.Sel("currentNotificationCenter"))
	}
	// unRegisterDelegate registers the delegate class and returns an instance.
	unRegisterDelegate = registerDelegate
)

func (r *realCenter) bundled() bool {
	if err := appKitReady(); err != nil {
		return false
	}
	return mainBundleID() != ""
}

// setup loads the framework and registers the delegate once.
func (r *realCenter) setup() error {
	r.once.Do(func() {
		if err := unLoad(); err != nil {
			r.err = fmt.Errorf("tray: loading UserNotifications: %w", err)
			return
		}
		r.center = unCurrentCenter()
		if r.center == 0 {
			r.err = ErrNoNotifier
			return
		}
		delegate, err := unRegisterDelegate()
		if err != nil {
			r.err = fmt.Errorf("%w: %v", ErrNoTargetClass, err)
			return
		}
		r.center.Send(objc.Sel("setDelegate:"), delegate) // weak on Apple's side; delegate lives for the process
	})
	return r.err
}

// registerDelegate registers the class whose instance receives the center's
// callbacks and returns one instance.
func registerDelegate() (objc.ID, error) {
	{
		cls, err := objc.RegisterClass(
			"GoWidgetsTrayNotificationDelegate",
			objc.GetClass("NSObject"),
			[]objc.MethodDef{
				{
					// Present while the app is frontmost too.
					Cmd: objc.Sel("userNotificationCenter:willPresentNotification:withCompletionHandler:"),
					Fn: func(self objc.ID, _cmd objc.SEL, center, note, handler objc.ID) {
						objc.Block(handler).Invoke(uint64(unPresentAll))
					},
				},
				{
					Cmd: objc.Sel("userNotificationCenter:didReceiveNotificationResponse:withCompletionHandler:"),
					Fn: func(self objc.ID, _cmd objc.SEL, center, resp, handler objc.ID) {
						req := resp.Send(objc.Sel("notification")).Send(objc.Sel("request"))
						if id := objc.GoString(req.Send(objc.Sel("identifier"))); id != "" {
							if fn := unClicks.take(id); fn != nil {
								go fn()
							}
						}
						objc.Block(handler).Invoke()
					},
				},
			},
		)
		if err != nil {
			return 0, err
		}
		return objc.ID(cls).Send(objc.Sel("alloc")).Send(objc.Sel("init")), nil
	}
}

func (r *realCenter) authorize() error {
	if err := r.setup(); err != nil {
		return err
	}
	ch := make(chan authAnswer, 1)
	blk := objc.NewBlock(func(_ objc.Block, granted bool, e objc.ID) {
		a := authAnswer{granted: granted}
		if e != 0 {
			a.failed, a.text = true, nsErrorText(e) // copied here: e dies with the block
		}
		ch <- a
	})
	defer blk.Release()
	r.center.Send(objc.Sel("requestAuthorizationWithOptions:completionHandler:"), uint64(unAuthSoundAlert), blk)
	select {
	case a := <-ch:
		return a.err()
	case <-time.After(authTimeout):
		return fmt.Errorf("%w: no answer to the system prompt within %v", ErrNotAuthorized, authTimeout)
	}
}

// authAnswer is what the authorization completion handler reported.
type authAnswer struct {
	granted bool
	failed  bool
	text    string
}

func (a authAnswer) err() error {
	if a.failed {
		return fmt.Errorf("%w: %s", ErrNotAuthorized, a.text)
	}
	if !a.granted {
		return ErrNotAuthorized
	}
	return nil
}

// postAnswer is the text a post completion handler reports for its NSError:
// "" for none (accepted), else the refusal with the error's own text.
func postAnswer(e objc.ID, text func(objc.ID) string) string {
	if e == 0 {
		return ""
	}
	return "refused: " + text(e)
}

func (r *realCenter) post(id, title, body string) error {
	if err := r.setup(); err != nil {
		return err
	}
	var perr error
	objc.AutoreleasePool(func() {
		content := objc.ClassID("UNMutableNotificationContent").Send(objc.Sel("alloc")).Send(objc.Sel("init"))
		defer content.Send(objc.Sel("release"))
		content.Send(objc.Sel("setTitle:"), objc.NSString(title))
		content.Send(objc.Sel("setBody:"), objc.NSString(body))
		content.Send(objc.Sel("setSound:"), objc.ClassID("UNNotificationSound").Send(objc.Sel("defaultSound")))
		req := objc.ClassID("UNNotificationRequest").Send(
			objc.Sel("requestWithIdentifier:content:trigger:"), objc.NSString(id), content, objc.ID(0))

		ch := make(chan string, 1) // "" = accepted
		blk := objc.NewBlock(func(_ objc.Block, e objc.ID) {
			ch <- postAnswer(e, nsErrorText) // text copied here: e dies with the block
		})
		defer blk.Release()
		r.center.Send(objc.Sel("addNotificationRequest:withCompletionHandler:"), req, blk)
		select {
		case msg := <-ch:
			if msg != "" {
				perr = errors.New("tray: the notification center " + msg)
			}
		case <-time.After(10 * time.Second):
			perr = errors.New("tray: the notification center did not answer")
		}
	})
	return perr
}

// nsErrorText is an NSError's localizedDescription. The text is copied inside
// the call: the NSError is autoreleased and gone once the block returns.
func nsErrorText(e objc.ID) string {
	return objc.GoString(e.Send(objc.Sel("localizedDescription")))
}
