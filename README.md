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
    tray.Checkbox("Notifications", true, func(on bool) { setNotify(on) }),
    tray.SubMenu("Recent", tray.NewMenu().Add(tray.Item("file.txt", nil))),
    tray.Separator(),
    tray.Item("Quit", func() { t.Quit() }),
)

t := tray.New(iconPNG).SetTooltip("My App").SetMenu(menu)
t.OnReady(func() { /* live */ })
t.Run() // blocks on the platform event loop until Quit
```

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
  - **windows** / **linux** — implemented and compile-verified; runtime
    confirmation pending.
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
