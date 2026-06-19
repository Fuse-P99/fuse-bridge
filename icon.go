package main

import (
	"bytes"
	_ "embed"
	"image/png"

	"github.com/lxn/walk"
)

//go:embed FuseIcon.png
var fuseIconBytes []byte

//go:embed FuseIconConn.png
var fuseIconConnBytes []byte

//go:embed FuseIconDisconn.png
var fuseIconDisconnBytes []byte

var (
	iconStartup      *walk.Icon
	iconConnected    *walk.Icon
	iconDisconnected *walk.Icon
)

func initIcons() {
	iconStartup, _ = iconFromPNG(fuseIconBytes)
	iconConnected, _ = iconFromPNG(fuseIconConnBytes)
	iconDisconnected, _ = iconFromPNG(fuseIconDisconnBytes)
}

func iconFromPNG(data []byte) (*walk.Icon, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return walk.NewIconFromImage(img)
}
