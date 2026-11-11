//go:build ignore

// Command icongen draws the app icons from the same shapes as the favicon in
// index.html (an orange tile with a white speaker): the web app icons in
// static/, the icon of the Windows program (cmd/z407/z407.ico) and the icon
// of the macOS app (packaging/macos/z407.icns). Run it
// with "go generate ./..." after changing the design; that also rebuilds
// the Windows resource file from the new .ico (see cmd/z407/main.go).
package main

import (
	"bytes"
	"encoding/binary"
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

// writeICO writes a Windows icon holding one PNG image per size.
func writeICO(name string, sizes []int) error {
	var images [][]byte
	for _, size := range sizes {
		var b bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, draw(size, true)); err != nil {
			return err
		}
		images = append(images, b.Bytes())
	}
	var out bytes.Buffer
	le := func(v any) { _ = binary.Write(&out, binary.LittleEndian, v) }
	le([]uint16{0, 1, uint16(len(sizes))}) // reserved, type 1 = icon, count
	offset := uint32(6 + 16*len(sizes))
	for i, size := range sizes {
		// width and height (0 means 256), palette size, reserved
		le([]uint8{uint8(size), uint8(size), 0, 0})
		le([]uint16{1, 32}) // color planes, bits per pixel
		le([]uint32{uint32(len(images[i])), offset})
		offset += uint32(len(images[i]))
	}
	for _, img := range images {
		out.Write(img)
	}
	return os.WriteFile(name, out.Bytes(), 0o644)
}

// writeICNS writes a macOS icon holding one PNG image per size.
func writeICNS(name string) error {
	var body bytes.Buffer
	for _, e := range []struct {
		kind string
		size int
	}{
		{"ic11", 32}, {"ic12", 64}, {"ic07", 128}, {"ic13", 256}, // 16@2x, 32@2x, 128, 128@2x
		{"ic08", 256}, {"ic14", 512}, {"ic09", 512}, {"ic10", 1024}, // 256, 256@2x, 512, 512@2x
	} {
		var b bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, draw(e.size, true)); err != nil {
			return err
		}
		body.WriteString(e.kind)
		_ = binary.Write(&body, binary.BigEndian, uint32(8+b.Len()))
		body.Write(b.Bytes())
	}
	var out bytes.Buffer
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return os.WriteFile(name, out.Bytes(), 0o644)
}

func main() {
	if err := writeICNS("../../packaging/macos/z407.icns"); err != nil {
		log.Fatal(err)
	}
	if err := writeICO("../../cmd/z407/z407.ico", []int{16, 24, 32, 48, 64, 256}); err != nil {
		log.Fatal(err)
	}
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
