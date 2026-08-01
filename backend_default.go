package tray

// defaultBackend selects the platform backend. The native backends (darwin
// NSStatusItem, windows Shell_NotifyIcon, linux StatusNotifierItem) are added
// as build-tagged files; until one is present for the current OS this returns
// nil and callers must supply one via WithBackend (eg. NewHeadless). Run then
// reports ErrNoBackend rather than crashing.
func defaultBackend() Backend { return nil }
