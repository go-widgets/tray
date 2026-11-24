# tray — cross-platform system tray for go-widgets

A system-tray / menu-bar icon with menus, submenus, checkboxes and separators.
A tray is OS-integration, not a pixel-blitted widget, so it lives outside the
pure-blitting toolkit and drives the native APIs through a small `Backend`
interface — all `CGO_ENABLED=0`:

| platform | native API | mechanism |
|----------|-----------|-----------|
| darwin   | `NSStatusItem` + `NSMenu`/`NSMenuItem` | purego + the Objective-C runtime |
| windows  | `Shell_NotifyIcon` + `TrackPopupMenu`  | `golang.org/x/sys/windows` syscalls |
| linux    | `StatusNotifierItem` + `com.canonical.dbusmenu` | pure-Go DBus |

## Usage

```go
menu := tray.NewMenu().Add(
    tray.Item("Open", func() { open() }),
    tray.IconItem("Pause", pausePNG, func() { pause() }), // a glyph beside the label

    tray.Checkbox("Notifications", true, func(on bool) { setNotify(on) }),
    tray.SubMenu("Recent", tray.NewMenu().Add(tray.Item("file.txt", nil))),
    tray.Separator(),
    tray.Item("Quit", func() { t.Quit() }),
)

t := tray.New(iconPNG).SetTooltip("My App").SetMenu(menu)
t.OnReady(func() { /* live */ })
t.Run() // blocks on the platform event loop until Quit
```

### Beside a window: `Attach`

A program that already runs a window's event loop cannot also give `Run` its
main thread. `Attach` puts the same item up and **returns** instead of
blocking; `Quit` (or `Close`) takes it down again.

```go
if err := t.Attach(); err != nil { /* no tray here: say so */ }
defer t.Quit()
```

| backend | what `Attach` does |
|---------|--------------------|
| darwin  | joins the host's running `NSApplication`; call it once that loop runs |
| linux   | exports the StatusNotifierItem on the session bus and returns; godbus serves it from its own goroutines |
| windows | runs the icon's message loop on a thread of its own (a Win32 loop belongs to a thread, not the process) |
| headless, other platforms | `ErrNoBackend` |

`Attach` does not fire `OnReady` on any backend: the host's loop is what is
ready, and it already said so. go-widgets/application uses it for `Spec.Tray`.

### System notifications: `Notify`

A real OS notification (not the painted in-window toast), delivered even when
no window is open:

```go
err := t.Notify(tray.Notification{
    Title:   "Tunnel dropped",         // required, <= 63 characters
    Body:    "Reconnecting in 5 s",    // optional, <= 255 characters
    OnClick: func() { /* raise the window */ },
})
```

It returns once the platform has accepted the notification. Errors are typed:
`ErrEmptyTitle` / the bound errors from `Notification.Validate`,
`ErrNoNotifier`, `ErrNotRunning`, `ErrNotBundled`, `ErrNotAuthorized`. A
courtesy notification should log them and carry on.

| backend | what `Notify` does | requirement |
|---------|--------------------|-------------|
| darwin  | `UNUserNotificationCenter` (alert + sound); the delegate also shows it while the app is frontmost | the process must run from a **`.app` bundle with a `CFBundleIdentifier`** (a bare executable gets `ErrNotBundled`, not Apple's exception); the first call shows the system's authorization prompt and waits for the answer; a denial is `ErrNotAuthorized` and is asked again next time |
| linux   | `org.freedesktop.Notifications.Notify` on the tray's session-bus connection; `OnClick` rides the `default` action (`ActionInvoked`) | the tray is running (`Run`/`Attach`); the body is HTML-escaped when the server advertises `body-markup` |
| windows | a `Shell_NotifyIcon` balloon (`NIF_INFO`) on the tray's own icon, which Windows 10/11 draws as a toast, with no WinRT or Start-menu shortcut; `OnClick` is `NIN_BALLOONUSERCLICK` | the tray is running; only the latest balloon's click is delivered |
| headless | records in `Headless.Notified` | `Run` has started |

Control characters other than newline and tab are refused on every platform, so
the same call never succeeds on one OS and fails on another.

## Status

- **Core** (`Tray`, `Menu`, `MenuItem`, item activation/toggle, `Backend`
  interface, headless backend) — done, **100% covered**, builds on every arch.
- **Native backends are on by default.** `tray.New(icon).SetMenu(m).Run()` puts
  an icon in the menu bar of the platform you built for, with no build tag and
  nothing else to know.
  - **darwin** — `NSStatusItem` + `NSMenu` via ebitengine/purego, CGO=0.
    **Runtime-confirmed on a real macOS session**: the item is the thing that
    leaves the menu bar when the program stops and comes back when it starts,
    and clicking it opens its menu.
  - **linux** — the dbusmenu methods are tested in-process, under `-race`
    (the tree `Refresh` rebuilds is the one the bus reads, behind one lock),
    and CI runs `Attach` against a real session bus: the item's name is
    claimed, its menu answers `GetLayout`, and `Quit` withdraws it. What CI
    cannot show is a desktop shell drawing it.
  - **windows** — implemented and compile-verified; the icon and its menu were
    proven on a Windows 11 VM. `Attach` is compile-verified only.
  - windows and linux ignore `MenuItem.Icon`: a row carrying one draws as it
    did before, which is a gap, not a promise kept.
  - anything else — `defaultBackend` is nil and `Run` reports `ErrNoBackend`,
    which is the difference between "there is no tray here" and "your tray
    silently does nothing".

A caller that wants no native tray at all — a test, a headless service — passes
one in: `WithBackend(tray.NewHeadless())`.

### It used to need a build tag, and the tag was on the wrong thing

The native backends were opt-in behind `-tags tray_native`, so that the core
could keep a 100% coverage figure over the whole package. The cost was paid by
every caller: `Run` returned `ErrNoBackend`, nothing appeared anywhere, and
nothing said why. A program that did the obvious thing got a tray that quietly
did not exist.

The coverage gate now selects by SHAPE instead — everything that is not a
platform file (`_darwin`, `_linux`, `_windows`, `_android`, `_js`, `_other`) is
held at 100% — which is what the rest of this fleet does, and which gates a new
portable file the day it is written rather than the day somebody remembers it.
A library's default behaviour is not the place to keep its CI tidy.

BSD-3-Clause. Copyright the go-widgets authors.
