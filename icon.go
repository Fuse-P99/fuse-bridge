package main

import (
	"image"
	"image/color"
	"math"

	"github.com/lxn/walk"
)

var (
	iconConnected    *walk.Icon
	iconDisconnected *walk.Icon
)

func initIcons() {
	iconConnected, _ = makeCircleIcon(color.NRGBA{R: 34, G: 197, B: 94, A: 255})  // green
	iconDisconnected, _ = makeCircleIcon(color.NRGBA{R: 148, G: 163, B: 184, A: 255}) // slate grey
}

func makeCircleIcon(col color.NRGBA) (*walk.Icon, error) {
	const size = 16
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx := float64(size) / 2.0
	cy := float64(size) / 2.0
	radius := float64(size)/2.0 - 1.5

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			dist := math.Sqrt(dx*dx + dy*dy)
			if dist <= radius {
				img.Set(x, y, col)
			} else if dist <= radius+1.0 {
				// Soft anti-aliased edge
				alpha := uint8((radius + 1.0 - dist) * float64(col.A))
				img.Set(x, y, color.NRGBA{R: col.R, G: col.G, B: col.B, A: alpha})
			}
		}
	}
	return walk.NewIconFromImage(img)
}
