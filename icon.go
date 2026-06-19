package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	_ "image/png"

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
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	// walk.NewIconFromImage requires *image.NRGBA; normalize regardless of
	// the source color model (RGBA, paletted, gray, etc.)
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Src)
	return walk.NewIconFromImage(dst)
}
