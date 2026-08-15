package imgstore_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"strings"
	"testing"

	"wishd/internal/imgstore"
)

func newStore(t *testing.T) *imgstore.Store {
	t.Helper()
	st, err := imgstore.New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return st
}

func sampleJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func TestStoreDedupesAndServesDisplaySize(t *testing.T) {
	st := newStore(t)
	raw := sampleJPEG(t, 2000, 1200)

	a, err := st.Store(raw)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b, err := st.Store(raw)
	if err != nil {
		t.Fatalf("store again: %v", err)
	}
	if a.SHA256 != b.SHA256 {
		t.Error("identical bytes produced different content addresses")
	}
	if a.Width != 2000 || a.Height != 1200 {
		t.Errorf("dimensions = %dx%d, want 2000x1200", a.Width, a.Height)
	}

	display, mime, err := st.Open(a.SHA256, true)
	if err != nil {
		t.Fatalf("open display: %v", err)
	}
	defer display.Close()
	if mime != "image/jpeg" {
		t.Errorf("mime = %q", mime)
	}
	cfg, _, err := image.DecodeConfig(display)
	if err != nil {
		t.Fatalf("decode display: %v", err)
	}
	if cfg.Width != imgstore.DisplayLongEdge {
		t.Errorf("display long edge = %d, want %d", cfg.Width, imgstore.DisplayLongEdge)
	}

	original, _, err := st.Open(a.SHA256, false)
	if err != nil {
		t.Fatalf("open original: %v", err)
	}
	defer original.Close()
	ocfg, _, err := image.DecodeConfig(original)
	if err != nil {
		t.Fatalf("decode original: %v", err)
	}
	if ocfg.Width != 2000 {
		t.Errorf("original width = %d, want the untouched 2000", ocfg.Width)
	}
}

// TestReencodeStripsTrailingPayload is the polyglot case from plan §6: bytes
// are decoded and re-encoded, so anything smuggled alongside the image does
// not survive.
func TestReencodeStripsTrailingPayload(t *testing.T) {
	st := newStore(t)
	raw := sampleJPEG(t, 64, 64)
	polyglot := append(append([]byte{}, raw...), []byte("<script>alert('pwn')</script>")...)

	stored, err := st.Store(polyglot)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	f, _, err := st.Open(stored.SHA256, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	onDisk, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "script") {
		t.Error("the appended payload survived re-encoding")
	}
}

func TestRejectsNonImages(t *testing.T) {
	st := newStore(t)
	if _, err := st.Store([]byte("#!/bin/sh\necho not an image\n")); err == nil {
		t.Error("a non-image was accepted")
	}
}

func TestOpenRejectsPathTraversal(t *testing.T) {
	st := newStore(t)
	for _, bad := range []string{"../../etc/passwd", "", "zz", strings.Repeat("g", 64)} {
		if _, _, err := st.Open(bad, false); err == nil {
			t.Errorf("Open(%q) should have failed", bad)
		}
	}
}
