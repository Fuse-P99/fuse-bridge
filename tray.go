package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/png"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed frontend/public/FuseIcon2.png
var fuseTrayIconBytes []byte

// squarePNG pads a PNG to a centered square canvas. Windows turns raw PNG
// bytes into an HICON via CreateIconFromResourceEx, which assumes square icon
// images — FuseIcon2.png is 260x255, so feed it a squared copy. Returns the
// input unchanged if it doesn't decode or is already square.
func squarePNG(data []byte) []byte {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == h {
		return data
	}
	side := max(w, h)
	dst := image.NewNRGBA(image.Rect(0, 0, side, side))
	offset := image.Pt((side-w)/2, (side-h)/2)
	draw.Draw(dst, b.Sub(b.Min).Add(offset), src, b.Min, draw.Src)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return data
	}
	return out.Bytes()
}

// setupTray creates the system tray icon and its menu on the v3 application.
// Called from runWails before app.Run; the framework starts the tray with the
// main loop. Left-click and the "Open" menu item show the settings window;
// "Quit" exits the app (app.Run returns and main() finishes cleanup).
func setupTray(app *application.App) {
	tray := app.SystemTray.New()
	tray.SetIcon(squarePNG(fuseTrayIconBytes))
	tray.SetTooltip("Fuse Bridge")

	menu := app.Menu.New()
	menu.Add("Open").OnClick(func(*application.Context) { wailsApp.Show() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		MarkAppQuitting() // keep the overlay open-set intact for next launch
		app.Quit()
	})
	tray.SetMenu(menu)

	tray.OnClick(func() { wailsApp.Show() })
}

// SetTrayStatus is intentionally a no-op: the tray tooltip stays "Fuse Bridge"
// regardless of EQ/connection state. Kept so existing callers still compile.
func SetTrayStatus(status string) {}

// SetTrayConnected is intentionally a no-op: the tray icon is static and no
// longer reflects connection state. Kept so existing callers still compile.
func SetTrayConnected(connected bool) {}
