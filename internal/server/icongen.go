//go:build ignore

// Command icongen draws the app icons in static/ from the same shapes as the
// favicon in index.html (an orange tile with a white speaker). Run it with
// "go generate ./internal/server" after changing the design.
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

// The drawing uses the favicon's 32-unit coordinate system.
var (
	speaker = [][2]float64{{9, 13}, {9, 19}, {13, 19}, {18, 23}, {18, 9}, {13, 13}}
	orange  = color.NRGBA{0xf0, 0x65, 0x2a, 0xff}
)

const (
	halfStroke = 1.0 // stroke-width 2
	corner     = 7.0
	// sound wave: arc of radius 5 around (18,16) from (21,12) to (21,20)
	arcX, arcY, arcR = 18.0, 16.0, 5.0
)

func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

func onGlyph(x, y float64) bool {
	for i := range speaker {
		a, b := speaker[i], speaker[(i+1)%len(speaker)]
		if segDist(x, y, a[0], a[1], b[0], b[1]) <= halfStroke {
			return true
		}
	}
	limit := math.Atan2(4, 3)
	if ang := math.Atan2(y-arcY, x-arcX); math.Abs(ang) <= limit {
		return math.Abs(math.Hypot(x-arcX, y-arcY)-arcR) <= halfStroke
	}
	return math.Hypot(x-21, y-12) <= halfStroke || math.Hypot(x-21, y-20) <= halfStroke
}

func inTile(x, y float64, rounded bool) bool {
	if !rounded {
		return true
	}
	cx := math.Max(corner-x, math.Max(x-(32-corner), 0))
	cy := math.Max(corner-y, math.Max(y-(32-corner), 0))
	return math.Hypot(cx, cy) <= corner
}

func draw(size int, rounded bool) image.Image {
	const ss = 4 // supersampling per axis
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+.5)/ss) * 32 / float64(size)
					y := (float64(py) + (float64(sy)+.5)/ss) * 32 / float64(size)
					switch {
					case !inTile(x, y, rounded):
					case onGlyph(x, y):
						r, g, b, a = r+255, g+255, b+255, a+1
					default:
						r, g, b, a = r+float64(orange.R), g+float64(orange.G), b+float64(orange.B), a+1
					}
				}
			}
			if a > 0 {
				img.SetNRGBA(px, py, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(255 * a / (ss * ss))})
			}
		}
	}
	return img
}

func main() {
	for _, ic := range []struct {
		name    string
		size    int
		rounded bool
	}{
		{"static/icon-192.png", 192, true},
		{"static/icon-512.png", 512, true},
		// Maskable icons are cropped by the launcher, so the tile fills the square.
		{"static/icon-maskable-512.png", 512, false},
	} {
		f, err := os.Create(ic.name)
		if err != nil {
			log.Fatal(err)
		}
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(f, draw(ic.size, ic.rounded)); err != nil {
			log.Fatal(err)
		}
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
	}
}
