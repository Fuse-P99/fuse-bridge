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

//go:embed frontend/public/FuseIcon2.png
var fuseTrayIconBytes []byte

var (
	appIcon  *walk.Icon // loaded from PE resource ID 1 — used for dialog title bars
	iconTray *walk.Icon // the app's Fuse icon, used for the system tray
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
	iconTray, _ = iconFromPNG(fuseTrayIconBytes)
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
