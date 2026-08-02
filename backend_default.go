//go:build !tray_native

package tray

// defaultBackend returns nil in the default build: the platform-agnostic core
// is what CI covers to 100%. Build (or `go run`) your app with `-tags
// tray_native` to link the native backend for your OS (darwin NSStatusItem via
// purego, windows Shell_NotifyIcon, linux StatusNotifierItem over DBus). Without
// it, Run reports ErrNoBackend; supply one explicitly via WithBackend.
func defaultBackend() Backend { return nil }
