package imgstore_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"wishbone/internal/imgstore"
)

// halves is a w×h picture, red on its left half and blue on its right, so
// which way it was turned can be read back off the stored file.
func halves(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{R: 220, A: 255}
			if x >= w/2 {
				c = color.RGBA{B: 220, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// withOrientation inserts an EXIF APP1 segment carrying the given Orientation
// straight after the SOI marker, the way a phone camera writes it.
func withOrientation(raw []byte, orientation uint16, bo binary.ByteOrder) []byte {
	var tiff bytes.Buffer
	if bo == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	_ = binary.Write(&tiff, bo, uint16(42))
	_ = binary.Write(&tiff, bo, uint32(8)) // IFD0 follows the header
	_ = binary.Write(&tiff, bo, uint16(1)) // one entry
	_ = binary.Write(&tiff, bo, uint16(0x0112))
	_ = binary.Write(&tiff, bo, uint16(3)) // SHORT
	_ = binary.Write(&tiff, bo, uint32(1))
	_ = binary.Write(&tiff, bo, orientation)
	_ = binary.Write(&tiff, bo, uint16(0)) // value-field padding
	_ = binary.Write(&tiff, bo, uint32(0)) // no next IFD

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)

	out := append([]byte{}, raw[:2]...)
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

func storedImage(t *testing.T, st *imgstore.Store, sha string) image.Image {
	t.Helper()
	f, _, err := st.Open(sha, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode stored: %v", err)
	}
	return img
}

func isRed(c color.Color) bool {
	r, _, b, _ := c.RGBA()
	return r > 0x8000 && b < 0x4000
}

// TestStoreTurnsPhotosUpright: the stored picture is the way up the camera was
// held, not the way the sensor read it out, in either byte order.
func TestStoreTurnsPhotosUpright(t *testing.T) {
	const w, h = 64, 32
	type probe struct {
		x, y int
		red  bool
	}
	cases := []struct {
		orientation uint16
		w, h        int
		probes      []probe // where red should and should not be, after turning
	}{
		{1, w, h, []probe{{4, 16, true}, {60, 16, false}}},
		{2, w, h, []probe{{4, 16, false}, {60, 16, true}}},
		{3, w, h, []probe{{4, 16, false}, {60, 16, true}}},
		// The quarter turns swap the axes; the red left half ends up on top
		// for a clockwise turn and on the bottom for a counterclockwise one.
		{6, h, w, []probe{{16, 4, true}, {16, 60, false}}},
		{8, h, w, []probe{{16, 4, false}, {16, 60, true}}},
	}
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, c := range cases {
			st := newStore(t)
			stored, err := st.Store(withOrientation(halves(t, w, h), c.orientation, bo))
			if err != nil {
				t.Fatalf("orientation %d: store: %v", c.orientation, err)
			}
			if stored.Width != c.w || stored.Height != c.h {
				t.Errorf("%v orientation %d: stored %dx%d, want %dx%d",
					bo, c.orientation, stored.Width, stored.Height, c.w, c.h)
			}
			img := storedImage(t, st, stored.SHA256)
			for _, p := range c.probes {
				if got := isRed(img.At(p.x, p.y)); got != p.red {
					t.Errorf("%v orientation %d: red at (%d,%d) = %v, want %v",
						bo, c.orientation, p.x, p.y, got, p.red)
				}
			}
		}
	}
}

// TestStoreIgnoresBrokenOrientation: a mangled EXIF block is not a reason to
// lose the picture.
func TestStoreIgnoresBrokenOrientation(t *testing.T) {
	raw := withOrientation(halves(t, 64, 32), 6, binary.BigEndian)
	// Point IFD0 past the end of the block.
	i := bytes.Index(raw, []byte("Exif\x00\x00MM"))
	binary.BigEndian.PutUint32(raw[i+6+4:], 0xFFFFFF)
	stored, err := newStore(t).Store(raw)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if stored.Width != 64 {
		t.Errorf("width = %d, want the unturned 64", stored.Width)
	}
}

// TestStoreRefusesTooManyPixels: the area is checked from the header, before
// anything is decoded, so a small file claiming an enormous picture costs
// nothing. This PNG is an IHDR and nothing else.
func TestStoreRefusesTooManyPixels(t *testing.T) {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 10000)
	binary.BigEndian.PutUint32(ihdr[4:], 10000)
	ihdr[8], ihdr[9] = 8, 2 // 8-bit RGB
	var png bytes.Buffer
	png.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&png, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	png.Write(chunk)
	_ = binary.Write(&png, binary.BigEndian, crc32.ChecksumIEEE(chunk))

	_, err := newStore(t).Store(png.Bytes())
	if !errors.Is(err, imgstore.ErrTooManyPixels) {
		t.Errorf("err = %v, want ErrTooManyPixels", err)
	}
}
