# icongen

Draws the Wishbone app icons: the PNG sizes a web manifest and iOS need, which
cannot be SVGs.

It is a **separate module** on purpose — the rasterizer it depends on has no
business in the application's `go.mod`, and `go build ./...` at the repository
root ignores nested modules. The generated PNGs are committed, so you only need
this if the icon design changes.

```sh
cd tools/icongen
go run . ../../internal/web/static
```

It draws the geometry directly rather than rasterizing `icon.svg`, because
oksvg does not scale stroke widths with the render target, and fattening the
stroke in the SVG to compensate would break it in browsers, which scale it
correctly. **The coordinates in `main.go` are the same numbers as `icon.svg` —
change one and change the other.**

Two things about the shape are load-bearing:

* Only the left half is authored; the right half is its mirror. Hand-authoring
  both is how a bow ends up subtly lopsided.
* The loops and tails are stroked as **separate sub-paths**. A bow crosses itself
  at the knot, and a single self-intersecting outline makes the stroker fill the
  wrong side of it — the first attempt rendered a solid blob.

Outputs, all full-bleed because every platform applies its own mask:

| File | Used by |
|---|---|
| `icon-192.png`, `icon-512.png` | web manifest |
| `icon-512-maskable.png` | Android launchers, artwork inset to the safe zone |
| `apple-touch-icon.png` | iOS home screen (iOS rounds it itself — do not pre-round) |
