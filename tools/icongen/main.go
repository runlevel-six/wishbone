// One-off tool: draw the Wishbone icon at the PNG sizes a web manifest and iOS
// need. Lives outside the repo so no rasterizer dependency lands in go.mod —
// the generated PNGs are committed instead.
//
// It draws the geometry directly rather than rasterizing icon.svg, because
// oksvg does not scale stroke widths with the render target, and fattening the
// stroke in the SVG to compensate would break it in browsers, which scale it
// correctly. The path below mirrors icon.svg by hand; keep the two in step.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// Geometry in the SVG's 32-unit coordinate space.
const (
	viewBox     = 32.0
	strokeWidth = 3.1
)

// The furcula: two legs splaying from a joint at the bottom, as cubic beziers.
var legs = [][6]float64{
	{8.2, 7.6, 7.4, 11.4, 9.4, 15},     // left leg, upper curve
	{11.4, 18.6, 14.6, 20.8, 16, 26.2}, // left leg down to the joint
	{17.4, 20.8, 20.6, 18.6, 22.6, 15}, // right leg up
	{24.6, 11.4, 23.8, 7.6, 20.6, 6.6}, // right leg tip
}

const startX, startY = 11.4, 6.6

var (
	green = color.RGBA{0x1f, 0x6b, 0x4f, 0xff}
	bone  = color.RGBA{0xf7, 0xf5, 0xf2, 0xff}
)

func main() {
	out := os.Args[1]

	for _, t := range []struct {
		size     int
		name     string
		maskable bool
	}{
		{192, "icon-192.png", false},
		{512, "icon-512.png", false},
		{512, "icon-512-maskable.png", true},
		{180, "apple-touch-icon.png", false},
	} {
		// Platforms apply their own mask — iOS rounds apple-touch-icon, Android
		// crops maskable icons — so the background is drawn full-bleed and the
		// artwork is inset instead of pre-rounding the corners.
		artScale := 1.0
		if t.maskable {
			artScale = 0.8 // keep the artwork inside Android's safe zone
		}

		img := image.NewRGBA(image.Rect(0, 0, t.size, t.size))
		draw.Draw(img, img.Bounds(), &image.Uniform{green}, image.Point{}, draw.Src)

		s := float64(t.size) / viewBox * artScale
		offset := (float64(t.size) - viewBox*s) / 2
		at := func(x, y float64) fixed.Point26_6 {
			return rasterx.ToFixedP(x*s+offset, y*s+offset)
		}

		scanner := rasterx.NewScannerGV(t.size, t.size, img, img.Bounds())
		scanner.SetColor(bone)
		stroker := rasterx.NewStroker(t.size, t.size, scanner)
		stroker.SetStroke(
			fixed.Int26_6(strokeWidth*s*64), 4*64,
			rasterx.RoundCap, rasterx.RoundCap, rasterx.RoundGap, rasterx.ArcClip)

		stroker.Start(at(startX, startY))
		for _, c := range legs {
			stroker.CubeBezier(at(c[0], c[1]), at(c[2], c[3]), at(c[4], c[5]))
		}
		stroker.Stop(false)
		stroker.Draw()

		f, err := os.Create(out + "/" + t.name)
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, img); err != nil {
			panic(err)
		}
		f.Close()
		fmt.Printf("wrote %s (%dx%d, maskable=%v)\n", t.name, t.size, t.size, t.maskable)
	}
}
