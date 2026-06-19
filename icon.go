package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	_ "image/png"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

//go:embed tray-uncolored.png
var fuseIconBytes []byte

//go:embed FuseIconConn.png
var fuseIconConnBytes []byte

//go:embed FuseIconDisconn.png
var fuseIconDisconnBytes []byte

var (
	appIcon          *walk.Icon // FuseIcon.png — used for dialog title bars
	iconStartup      *walk.Icon
	iconConnected    *walk.Icon
	iconDisconnected *walk.Icon
)

// applyDialogIcon sets both the small (title bar) and big (taskbar) icon on a
// dialog. walk's SetIcon only sends ICON_SMALL, so we send ICON_BIG manually.
func applyDialogIcon(dlg *walk.Dialog) {
	if appIcon != nil {
		dlg.SetIcon(appIcon)
	}
	hIcon := win.LoadIcon(win.GetModuleHandle(nil), win.MAKEINTRESOURCE(1))
	if hIcon != 0 {
		win.SendMessage(dlg.Handle(), win.WM_SETICON, 1 /* ICON_BIG */, uintptr(hIcon))
	}
}

func initIcons() {
	appIcon, _ = walk.NewIconFromResourceId(1) // FuseIcon multi-size ICO embedded via rsrc.syso
	iconStartup, _ = iconFromPNG(fuseIconBytes)
	iconConnected, _ = iconFromPNG(fuseIconConnBytes)
	iconDisconnected, _ = iconFromPNG(fuseIconDisconnBytes)
}

func iconFromPNG(data []byte) (*walk.Icon, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Src)
	return walk.NewIconFromImage(dst)
}
