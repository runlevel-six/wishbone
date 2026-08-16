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
correctly. **The path in `main.go` mirrors `icon.svg` by hand — change one and
change the other.**

Outputs, all full-bleed because every platform applies its own mask:

| File | Used by |
|---|---|
| `icon-192.png`, `icon-512.png` | web manifest |
| `icon-512-maskable.png` | Android launchers, artwork inset to the safe zone |
| `apple-touch-icon.png` | iOS home screen (iOS rounds it itself — do not pre-round) |
