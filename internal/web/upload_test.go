package web

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"wishbone/internal/auth"
	"wishbone/internal/imgstore"
)

// uploadPart is one file sent as "image". An empty body is the input the
// person did not use, which a browser still sends.
type uploadPart struct {
	name string
	body []byte
}

// postItemWithPictures submits the add-item form the way a browser does,
// multipart, with one part per picture input in page order.
func (h *harness) postItemWithPictures(parts ...uploadPart) *httptest.ResponseRecorder {
	h.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("title", "Something with no link")
	_ = mw.WriteField("quantity", "1")
	for _, p := range parts {
		fw, err := mw.CreateFormFile("image", p.name)
		if err != nil {
			h.t.Fatal(err)
		}
		_, _ = fw.Write(p.body)
	}
	if err := mw.Close(); err != nil {
		h.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/lists/"+h.list.ID+"/items", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: h.ownerSession})
	req.Header.Set(csrfHeader, auth.CSRFToken(h.cfg.SecretKey, auth.HashToken(h.ownerSession)))
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		h.t.Fatalf("create returned %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func flashText(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == flashCookie {
			v, _ := url.QueryUnescape(c.Value)
			return v
		}
	}
	return ""
}

func smallJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (h *harness) newestItemImages() int {
	h.t.Helper()
	imgs, err := h.st.ImagesForItem(context.Background(), h.newestItem().ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return len(imgs)
}

// TestUploadKeepsWhicheverInputWasUsed: the form has a camera input and a
// files input, both named "image". The unused one arrives as an empty part,
// and it comes first when the files input was the one used.
func TestUploadKeepsWhicheverInputWasUsed(t *testing.T) {
	h := newHarness(t)
	rec := h.postItemWithPictures(uploadPart{"", nil}, uploadPart{"chosen.jpg", smallJPEG(t)})
	if n := h.newestItemImages(); n != 1 {
		t.Errorf("item has %d pictures, want the one chosen", n)
	}
	if got := flashText(rec); !strings.HasSuffix(got, "|Added.") {
		t.Errorf("flash = %q, want a plain Added.", got)
	}
}

// TestUploadThatCannotBeUsedSaysSo: before this, a photo over the limit was
// cut short, failed to decode, and the item saved without it and without a
// word. The item still saves; the flash now says what happened and what to do.
func TestUploadThatCannotBeUsedSaysSo(t *testing.T) {
	oversized := append(smallJPEG(t), make([]byte, imgstore.MaxImageBytes)...)
	for _, c := range []struct {
		name string
		body []byte
		want string
	}{
		{"too big", oversized, "too big to save (the limit is 5 MB)"},
		{"not a picture", []byte("this is not an image"), "could not be read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.postItemWithPictures(uploadPart{"photo.jpg", c.body}, uploadPart{"", nil})
			if n := h.newestItemImages(); n != 0 {
				t.Errorf("item has %d pictures, want none", n)
			}
			got := flashText(rec)
			if !strings.HasPrefix(got, "warn|Added. ") || !strings.Contains(got, c.want) {
				t.Errorf("flash = %q, want a warning containing %q", got, c.want)
			}
		})
	}
}

// TestPictureInputs: the camera input opens the camera on a phone, and both
// carry the limit app.js shrinks a photo to fit.
func TestPictureInputs(t *testing.T) {
	h := newHarness(t)
	body := h.get("/lists/"+h.list.ID+"/items/new", h.ownerSession).Body.String()
	shrink := `data-shrink="` + strconv.Itoa(imgstore.MaxImageBytes) + `"`
	if n := strings.Count(body, shrink); n != 2 {
		t.Errorf("%d inputs carry %s, want 2", n, shrink)
	}
	if n := strings.Count(body, `capture="environment"`); n != 1 {
		t.Errorf("%d inputs open the camera, want exactly 1", n)
	}
	if !strings.Contains(body, `class="picture-pick camera-only"`) {
		t.Error("the camera input is not marked camera-only, so a desktop would show it")
	}
}
