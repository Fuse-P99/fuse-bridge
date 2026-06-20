package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	_ "image/png"
	"syscall"

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
	appIcon          *walk.Icon // loaded from PE resource ID 1 — used for dialog title bars
	iconStartup      *walk.Icon
	iconConnected    *walk.Icon
	iconDisconnected *walk.Icon
)

var setClassLongPtrW = syscall.NewLazyDLL("user32.dll").NewProc("SetClassLongPtrW")

// overrideClassIcon patches the window class registered by walk (which looks
// for icon resource ID 7 but rsrc embeds at ID 1) so the taskbar shows our
// custom icon. Must be called once per distinct walk window class.
func overrideClassIcon(hwnd win.HWND) {
	hInst := win.GetModuleHandle(nil)

	hIconBig := win.HICON(win.LoadImage(hInst, win.MAKEINTRESOURCE(2),
		win.IMAGE_ICON, 0, 0, win.LR_DEFAULTSIZE))
	hIconSm := win.HICON(win.LoadImage(hInst, win.MAKEINTRESOURCE(2),
		win.IMAGE_ICON, 16, 16, win.LR_DEFAULTCOLOR))

	// GCLP_HICON = -14, GCLP_HICONSM = -34; must be runtime int → uintptr
	nBig := int(-14)
	nSm := int(-34)

	if hIconBig != 0 {
		setClassLongPtrW.Call(uintptr(hwnd), uintptr(nBig), uintptr(hIconBig))
		win.SendMessage(hwnd, win.WM_SETICON, 1, uintptr(hIconBig))
	}
	if hIconSm != 0 {
		setClassLongPtrW.Call(uintptr(hwnd), uintptr(nSm), uintptr(hIconSm))
		win.SendMessage(hwnd, win.WM_SETICON, 0, uintptr(hIconSm))
	}
}

// applyDialogIcon sets the application icon on a dialog (title bar + taskbar).
func applyDialogIcon(dlg *walk.Dialog) {
	if appIcon != nil {
		dlg.SetIcon(appIcon) // DPI-aware small+big via walk
	}
	overrideClassIcon(dlg.Handle())
}

func initIcons() {
	appIcon, _ = walk.NewIconFromResourceId(2) // ID 2: manifest takes ID 1, icon group is ID 2
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
