package tray

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// Native system notifications.
//
// A notification is OS-integration for the same reason a tray icon is, and it
// is delivered by the same per-OS machinery: the desktop's own notification
// service, not a rectangle painted inside a window (toolkit's Toast is that, and
// it is invisible when the window is closed -- which is exactly when a menu-bar
// app has something to say).
//
//   - darwin:  UNUserNotificationCenter (needs an app BUNDLE, see [ErrNotBundled])
//   - linux:   org.freedesktop.Notifications over the session bus
//   - windows: a Shell_NotifyIcon balloon on the tray's own icon
//
// It hangs off the [Tray] and not off a package-level function because Windows
// has no notification without an icon to hang it on, and the other two reuse the
// tray's already-open connection (Linux) or its AppKit setup (macOS).

// Bounds on what a caller may send. They are the smallest of what the three
// platforms accept (Windows: 63 title and 255 body UTF-16 units), applied on
// every platform so the same call does not succeed on one OS and fail on another.
const (
	// MaxNotificationTitle is the longest Title, in characters (runes).
	MaxNotificationTitle = 63
	// MaxNotificationBody is the longest Body, in characters (runes).
	MaxNotificationBody = 255
)

var (
	// ErrNoNotifier is returned by [Tray.Notify] when the tray's backend cannot
	// post a notification (a custom Backend that does not implement [Notifier], or
	// a platform without a notification service).
	ErrNoNotifier = errors.New("tray: this backend cannot post notifications")
	// ErrNotRunning is returned when the tray has not been Run or Attached yet (or
	// has been Quit): there is no connection, window or icon to post through.
	ErrNotRunning = errors.New("tray: notifications need a running tray")
	// ErrNotBundled is returned on macOS when the process is not inside an app
	// bundle with a bundle identifier. UNUserNotificationCenter refuses (it raises
	// an Objective-C exception) for a bare executable, as `go run` produces.
	ErrNotBundled = errors.New("tray: macOS notifications need the app to run from a .app bundle with a CFBundleIdentifier")
	// ErrNotAuthorized is returned on macOS when the user has not allowed this
	// app to post notifications (System Settings > Notifications).
	ErrNotAuthorized = errors.New("tray: the user has not authorized notifications for this app")
	// ErrEmptyTitle is returned for a notification with no Title.
	ErrEmptyTitle = errors.New("tray: a notification needs a title")
)

// Notification is one system notification.
type Notification struct {
	// Title is the headline. Required; at most [MaxNotificationTitle] characters.
	Title string
	// Body is the detail line. Optional; at most [MaxNotificationBody] characters.
	Body string
	// OnClick, when non-nil, runs when the user clicks the notification. It runs
	// on a goroutine of the backend's, never on the caller's. It is a best effort:
	// the desktop may never deliver the click (the notification was dismissed,
	// timed out, or a notification daemon without actions is running), and
	// Windows only delivers it while the balloon is the latest one.
	OnClick func()
}

// Validate reports whether the notification is within the bounds above. Control
// characters other than newline and tab are refused: they are never display text
// and some notification daemons pass the string on to a markup parser.
func (n Notification) Validate() error {
	if strings.TrimSpace(n.Title) == "" {
		return ErrEmptyTitle
	}
	if err := checkText("title", n.Title, MaxNotificationTitle); err != nil {
		return err
	}
	return checkText("body", n.Body, MaxNotificationBody)
}

func checkText(what, s string, max int) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("tray: notification %s is not valid UTF-8", what)
	}
	if utf8.RuneCountInString(s) > max {
		return fmt.Errorf("tray: notification %s is longer than %d characters", what, max)
	}
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return fmt.Errorf("tray: notification %s holds a control character (U+%04X)", what, r)
		}
	}
	return nil
}

// Notifier is the optional Backend capability to post a system notification.
type Notifier interface {
	Notify(t *Tray, n Notification) error
}

// Notify posts a system notification and returns once the platform has accepted
// it (not when the user has seen it). The tray must be running -- see
// [ErrNotRunning] -- except on macOS where it only has to be bundled.
//
// Errors are typed: [ErrEmptyTitle] and the bound errors from
// [Notification.Validate], [ErrNoNotifier], [ErrNotRunning], [ErrNotBundled],
// [ErrNotAuthorized]. A caller whose notification is a courtesy (a tunnel
// dropped) should log and carry on rather than treat any of them as fatal.
func (t *Tray) Notify(n Notification) error {
	if err := n.Validate(); err != nil {
		return err
	}
	if t.backend == nil {
		return ErrNoBackend
	}
	nf, ok := t.backend.(Notifier)
	if !ok {
		return ErrNoNotifier
	}
	return nf.Notify(t, n)
}

// utf16Trunc returns units cut to at most max, never splitting a surrogate pair.
// The Windows balloon's buffers are fixed arrays, so the caller passes the array
// length minus one to leave room for the NUL. Kept platform-neutral so it is
// tested on every OS.
func utf16Trunc(units []uint16, max int) []uint16 {
	if len(units) <= max {
		return units
	}
	cut := max
	// units[cut-1] is the last kept unit; if it is a high surrogate its pair was cut.
	if cut > 0 && units[cut-1] >= 0xD800 && units[cut-1] < 0xDC00 {
		cut--
	}
	return units[:cut]
}

// balloonUnits is the text a Windows balloon shows, as UTF-16 truncated to the
// buffers NOTIFYICONDATAW has (title 64 units, body 256, each with its NUL). The
// validated bounds are in runes, and a rune outside the BMP is two units, so a
// notification that passed Validate can still need this cut.
func balloonUnits(n Notification) (title, info []uint16) {
	return utf16Trunc(utf16.Encode([]rune(n.Title)), 63), utf16Trunc(utf16.Encode([]rune(n.Body)), 255)
}

// maxPendingClicks bounds a click table: a notification that is never closed (a
// daemon that drops the signal) must not grow it for ever.
const maxPendingClicks = 64

// clickTable maps a notification's id (the server's uint32 on Linux, our own
// string on macOS) to the callback its click should run, oldest evicted first.
type clickTable[K comparable] struct {
	mu    sync.Mutex
	order []K
	fns   map[K]func()
}

func newClickTable[K comparable]() *clickTable[K] { return &clickTable[K]{fns: map[K]func(){}} }

func (c *clickTable[K]) add(id K, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.fns[id]; !dup {
		c.order = append(c.order, id)
	}
	c.fns[id] = fn
	for len(c.order) > maxPendingClicks {
		delete(c.fns, c.order[0])
		c.order = c.order[1:]
	}
}

// take removes and returns the callback for id, or nil.
func (c *clickTable[K]) take(id K) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn := c.fns[id]
	delete(c.fns, id)
	if i := slices.Index(c.order, id); i >= 0 {
		c.order = slices.Delete(c.order, i, i+1)
	}
	return fn
}
