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
	strokeWidth = 2.4
)

// subpath is one stroked run: a start point and a list of cubic beziers, each
// {c1x, c1y, c2x, c2y, x, y}.
type subpath struct {
	x, y  float64
	curve [][6]float64
}

// mirror reflects a subpath across the vertical centre line, which is where the
// mark's symmetry comes from: the left half is authored once and the right half
// is this function. Hand-authoring both halves is how a bow ends up subtly
// lopsided.
func (p subpath) mirror() subpath {
	m := subpath{x: viewBox - p.x, y: p.y}
	for _, c := range p.curve {
		m.curve = append(m.curve,
			[6]float64{viewBox - c[0], c[1], viewBox - c[2], c[3], viewBox - c[4], c[5]})
	}
	return m
}

// The left half of a ribbon bow: a loop from the knot out to the upper left and
// back, and a tail descending from the knot with a rounded tip. Mirrored, the
// two tails splay from the knot as a furcula — which is the whole point of the
// mark, so they are long and they stay long. Shortening them leaves an ordinary
// bow and takes the wishbone out of it.
//
// Each run is stroked on its own rather than chained into one path. A bow
// crosses itself at the knot, and a single self-intersecting outline makes the
// stroker fill the wrong side of it: the first attempt at this rendered a blob.
var halfBow = []subpath{
	{ // the loop: out to the upper left and back, enclosing an open eye
		x: 15.8, y: 12.2,
		curve: [][6]float64{
			{12.2, 7.0, 8.2, 4.6, 6.8, 8.0},     // upper edge, arcing high
			{5.8, 10.8, 10.8, 12.2, 15.4, 12.6}, // lower edge, back to the knot
		},
	},
	{ // the tail: down and out, crossing its twin just under the knot
		x: 16.3, y: 12.9,
		curve: [][6]float64{
			{13.8, 14.6, 10.6, 20.4, 10.1, 25.4},
		},
	},
}

var (
	green   = color.RGBA{0x1f, 0x4d, 0x3a, 0xff}
	ribbonC = color.RGBA{0xf7, 0xef, 0xe4, 0xff}
)

func main() {
	out := os.Args[1]

	for _, t := range []struct {
		size     int
		name     string
		maskable bool
	}{
		// A plain 32px PNG so a browser that will not render the SVG still has
		// an icon. Firefox needed one for years, and something always does.
		{32, "favicon-32.png", false},
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

		for _, p := range halves() {
			scanner := rasterx.NewScannerGV(t.size, t.size, img, img.Bounds())
			scanner.SetColor(ribbonC)
			stroker := rasterx.NewStroker(t.size, t.size, scanner)
			stroker.SetStroke(
				fixed.Int26_6(strokeWidth*s*64), 4*64,
				rasterx.RoundCap, rasterx.RoundCap, rasterx.RoundGap, rasterx.ArcClip)

			stroker.Start(at(p.x, p.y))
			for _, c := range p.curve {
				stroker.CubeBezier(at(c[0], c[1]), at(c[2], c[3]), at(c[4], c[5]))
			}
			stroker.Stop(false)
			stroker.Draw()
		}

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

// halves returns the authored left half plus its mirror image.
func halves() []subpath {
	out := make([]subpath, 0, len(halfBow)*2)
	for _, p := range halfBow {
		out = append(out, p, p.mirror())
	}
	return out
}
