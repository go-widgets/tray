// Command traydemo is a runnable go-widgets/tray example.
//
// On macOS, verify the native backend live:
//
//	go run ./examples/traydemo
//
// A menu-bar icon appears; clicking items prints to the terminal. Without the
// tag (or on a platform whose native backend isn't built yet) it prints
// ErrNoBackend and exits — the core is still fully exercised.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/go-widgets/tray"
)

func main() {
	var t *tray.Tray
	menu := tray.NewMenu().Add(
		tray.Item("Hello", func() { fmt.Println("hello clicked") }),
		tray.Checkbox("Notifications", true, func(on bool) { fmt.Println("notifications:", on) }),
		tray.SubMenu("Recent", tray.NewMenu().Add(
			tray.Item("file-1.txt", func() { fmt.Println("open file-1") }),
			tray.Item("file-2.txt", func() { fmt.Println("open file-2") }),
		)),
		tray.Separator(),
		tray.Item("Quit", func() { t.Quit() }),
	)

	t = tray.New(icon()).SetTooltip("go-widgets tray demo").SetMenu(menu)
	t.OnReady(func() { fmt.Println("tray ready") })

	if err := t.Run(); err != nil {
		fmt.Println("tray:", err, "(this platform has no native backend)")
	}
}

// icon draws a small filled square and PNG-encodes it, so the demo needs no
// asset files.
func icon() []byte {
	const n = 22
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.Set(x, y, color.NRGBA{R: 0x2b, G: 0x8a, B: 0xd6, A: 0xff})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
