package imgstore

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
)

// jpegOrientation returns the EXIF Orientation (1–8) of a JPEG, or 1 when
// there is none or it cannot be read.
//
// Phones write the sensor's pixels as they came off it and record which way up
// the camera was held in this tag. The decoder ignores it, and Store strips
// EXIF by re-encoding, so unless the rotation is applied before that a portrait
// photo is stored lying on its side — permanently, since the tag that could
// have corrected it is gone.
//
// Only the one tag is read. Pulling in an EXIF library for a single uint16
// would add a parser of attacker-supplied bytes far larger than this one.
func jpegOrientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return 1
	}
	p := 2
	for p+4 <= len(raw) {
		if raw[p] != 0xFF {
			return 1
		}
		marker := raw[p+1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			p += 2 // standalone markers carry no length
			continue
		}
		if marker == 0xDA || marker == 0xD9 {
			return 1 // start of scan: the metadata segments are all behind us
		}
		size := int(binary.BigEndian.Uint16(raw[p+2:]))
		if size < 2 || p+2+size > len(raw) {
			return 1
		}
		seg := raw[p+4 : p+2+size]
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffOrientation(seg[6:])
		}
		p += 2 + size
	}
	return 1
}

// tiffOrientation reads tag 0x0112 from IFD0 of an EXIF TIFF block.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd:]))
	for i := 0; i < n; i++ {
		e := ifd + 2 + 12*i
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) != 0x0112 {
			continue
		}
		// SHORT, count 1: the value sits in the first two bytes of the
		// value field, in the block's own byte order.
		if bo.Uint16(t[e+2:]) != 3 {
			return 1
		}
		if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}

// orientedSize is the width and height an image has once its orientation is
// applied: values 5–8 swap the axes.
func orientedSize(w, h, orientation int) (int, int) {
	if orientation >= 5 {
		return h, w
	}
	return w, h
}

// applyOrientation returns img turned upright for the given EXIF orientation,
// or img itself for 1.
func applyOrientation(img image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return img
	}
	// Flatten to RGBA once (draw has fast paths for the decoder's YCbCr), so
	// the turn itself is a byte copy rather than 12 million At/Set calls.
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	dw, dh := orientedSize(w, h, orientation)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orientation {
			case 2: // mirrored
				dx, dy = w-1-x, y
			case 3: // upside down
				dx, dy = w-1-x, h-1-y
			case 4: // upside down, mirrored
				dx, dy = x, h-1-y
			case 5: // transposed
				dx, dy = y, x
			case 6: // needs a quarter turn clockwise
				dx, dy = h-1-y, x
			case 7: // transversed
				dx, dy = h-1-y, w-1-x
			case 8: // needs a quarter turn counterclockwise
				dx, dy = y, w-1-x
			}
			si := src.PixOffset(x, y)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
