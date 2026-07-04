package main

import (
	"math"
	"sync"
	"time"
)

// PlayerPosition is the player's most recent /loc reading. Coordinates are EQ
// world coordinates (X: +West/-East, Y: +North/-South, Z: elevation). Heading is
// a compass bearing in degrees (0=North, 90=East), inferred from movement, or -1
// when not yet known.
type PlayerPosition struct {
	Zone    string  `json:"zone"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Z       float64 `json:"z"`
	Heading float64 `json:"heading"`
	Time    int64   `json:"time"` // unix millis
}

var (
	posMu        sync.RWMutex
	curPos       PlayerPosition
	havePos      bool
	prevX, prevY float64
	havePrev     bool

	currentZone string // the zone the local player is currently in
)

// SetCurrentZone records the zone the player just entered. Heading/position
// continuity is reset so a fresh /loc in the new zone doesn't infer a bogus
// heading from the previous zone's coordinates.
func SetCurrentZone(zone string) {
	posMu.Lock()
	currentZone = zone
	havePrev = false
	posMu.Unlock()
}

// CurrentZone returns the player's current zone (display/long name).
func CurrentZone() string {
	posMu.RLock()
	defer posMu.RUnlock()
	return currentZone
}

// UpdatePosition records a new /loc reading, inferring a compass heading from the
// movement delta versus the previous reading. Bearing math (world axes: +X=West,
// +Y=North): east component = -dx, north component = dy, so bearing = atan2(-dx, dy).
func UpdatePosition(x, y, z float64) {
	posMu.Lock()
	defer posMu.Unlock()

	heading := -1.0
	if havePos {
		heading = curPos.Heading
	}
	if havePrev {
		dx, dy := x-prevX, y-prevY
		if math.Hypot(dx, dy) > 0.5 { // ignore standing-still jitter
			h := math.Atan2(-dx, dy) * 180 / math.Pi
			if h < 0 {
				h += 360
			}
			heading = h
		}
	}
	prevX, prevY = x, y
	havePrev = true

	curPos = PlayerPosition{
		Zone:    currentZone,
		X:       x,
		Y:       y,
		Z:       z,
		Heading: heading,
		Time:    time.Now().UnixMilli(),
	}
	havePos = true
}

// GetPosition returns the most recent player position.
func GetPosition() PlayerPosition {
	posMu.RLock()
	defer posMu.RUnlock()
	return curPos
}
