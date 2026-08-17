// Package imgstore fetches, re-encodes and stores item images (plan §6).
//
// Two rules drive the design. Images are never hotlinked: links rot, and
// hotlinking leaks every viewer's IP to the retailer. And bytes are never
// stored verbatim: everything is decoded and re-encoded, which strips EXIF and
// makes a polyglot file (a valid image that is also a valid script) inert.
package imgstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"

	_ "image/gif" // decode-only

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode-only: many retailers serve webp now

	"wishbone/internal/fetch"
)

// MaxImageBytes caps an image download (plan §6).
const MaxImageBytes = 5 << 20 // 5 MiB

// DisplayLongEdge is the long edge of the generated display derivative.
const DisplayLongEdge = 1024

var ErrUnsupportedImage = errors.New("imgstore: unsupported or corrupt image")

// Stored describes a blob that is now on disk.
type Stored struct {
	SHA256 string
	Mime   string
	Width  int
	Height int
}

type Store struct {
	dir    string
	client *fetch.Client
}

func New(dir string, client *fetch.Client) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("imgstore: %w", err)
	}
	return &Store{dir: dir, client: client}, nil
}

// FetchAndStore downloads an image through the same guarded dialer as pages
// and stores it content-addressed.
func (s *Store) FetchAndStore(ctx context.Context, rawURL string) (*Stored, error) {
	if s.client == nil {
		return nil, errors.New("imgstore: fetching disabled")
	}
	resp, err := s.client.Get(ctx, rawURL, "image/*", "image/", MaxImageBytes)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("imgstore: status %d", resp.StatusCode)
	}
	if resp.Truncated {
		return nil, fmt.Errorf("imgstore: image exceeds %d bytes", MaxImageBytes)
	}
	return s.Store(resp.Body)
}

// Store re-encodes raw image bytes and writes the original plus one display
// derivative. The returned SHA is of the re-encoded canonical bytes, so two
// uploads of the same picture dedupe even if their containers differed.
func (s *Store) Store(raw []byte) (*Stored, error) {
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedImage, err)
	}

	canonical, mime, ext, err := encode(img, format)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	sha := hex.EncodeToString(sum[:])

	if err := s.writeFile(s.pathFor(sha, ext), canonical); err != nil {
		return nil, err
	}

	// Display derivative: long edge 1024, skipped when the image is already
	// smaller than that.
	if display := scaleLongEdge(img, DisplayLongEdge); display != nil {
		dbytes, _, _, err := encode(display, format)
		if err == nil {
			if err := s.writeFile(s.displayPathFor(sha, ext), dbytes); err != nil {
				return nil, err
			}
		}
	}

	b := img.Bounds()
	return &Stored{SHA256: sha, Mime: mime, Width: b.Dx(), Height: b.Dy()}, nil
}

// Open returns the display derivative when one exists, falling back to the
// original. It probes the known extensions rather than storing the extension
// separately, so the filesystem stays the single source of truth.
func (s *Store) Open(sha string, preferDisplay bool) (io.ReadCloser, string, error) {
	if !validSHA(sha) {
		return nil, "", os.ErrNotExist
	}
	type candidate struct{ path, mime string }
	var candidates []candidate
	for _, e := range []struct{ ext, mime string }{{"jpg", "image/jpeg"}, {"png", "image/png"}} {
		if preferDisplay {
			candidates = append(candidates, candidate{s.displayPathFor(sha, e.ext), e.mime})
		}
	}
	for _, e := range []struct{ ext, mime string }{{"jpg", "image/jpeg"}, {"png", "image/png"}} {
		candidates = append(candidates, candidate{s.pathFor(sha, e.ext), e.mime})
	}
	for _, c := range candidates {
		f, err := os.Open(c.path)
		if err == nil {
			return f, c.mime, nil
		}
	}
	return nil, "", os.ErrNotExist
}

// Remove deletes every file for a blob. Callers must check the reference count
// first — blobs are deduped across items.
func (s *Store) Remove(sha string) error {
	if !validSHA(sha) {
		return os.ErrNotExist
	}
	var firstErr error
	for _, ext := range []string{"jpg", "png"} {
		for _, p := range []string{s.pathFor(sha, ext), s.displayPathFor(sha, ext)} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Store) pathFor(sha, ext string) string {
	return filepath.Join(s.dir, sha[0:2], sha+"."+ext)
}

func (s *Store) displayPathFor(sha, ext string) string {
	return filepath.Join(s.dir, sha[0:2], sha+".d"+fmt.Sprint(DisplayLongEdge)+"."+ext)
}

func (s *Store) writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil // content-addressed: identical bytes, nothing to do
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// encode re-encodes a decoded image. PNG in, PNG out (transparency matters for
// product cutouts); everything else becomes JPEG.
func encode(img image.Image, format string) (data []byte, mime, ext string, err error) {
	var buf bytes.Buffer
	switch format {
	case "png", "gif":
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", "", err
		}
		return buf.Bytes(), "image/png", "png", nil
	default:
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", "", err
		}
		return buf.Bytes(), "image/jpeg", "jpg", nil
	}
}

// scaleLongEdge returns a resized copy, or nil when the image is already small
// enough to serve as-is.
func scaleLongEdge(img image.Image, longEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= longEdge && h <= longEdge {
		return nil
	}
	nw, nh := w, h
	if w >= h {
		nw = longEdge
		nh = h * longEdge / w
	} else {
		nh = longEdge
		nw = w * longEdge / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

func validSHA(sha string) bool {
	if len(sha) != 64 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
