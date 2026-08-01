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
- **Native backends** — the substantial next increment. A tray can only be
  verified on a live desktop session, so each backend ships with on-device
  confirmation rather than being asserted from headless CI. Until a backend is
  present for the current OS, `Run` returns `ErrNoBackend`; supply one (or the
  headless backend) via `WithBackend`.

BSD-3-Clause. Copyright the go-widgets authors.
