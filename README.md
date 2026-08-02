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
- **Native backends** are opt-in via the `tray_native` build tag, so the core
  keeps its 100% coverage gate while the native code is compile-verified per-OS
  in CI. A tray can only be *runtime*-verified on a live desktop session.
  - **darwin** — implemented: `NSStatusItem`+`NSMenu` via ebitengine/purego,
    CGO=0. Compile-verified; **runtime confirmation on a real macOS session is
    pending**. Build/run your app with `-tags tray_native`.
  - **windows** / **linux** — next increments (currently `nil` under the tag →
    `ErrNoBackend`).

Without the tag (or a native backend for the OS), `Run` returns `ErrNoBackend`;
supply one — including the headless backend — via `WithBackend`.

```sh
go run -tags tray_native ./cmd/yourapp   # link the macOS NSStatusItem backend
```

BSD-3-Clause. Copyright the go-widgets authors.
