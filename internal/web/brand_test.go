package web

import (
	"strings"
	"testing"
)

// TestMarkAppearsOnBothLayouts: the icon was drawn, generated at five sizes,
// wired into the manifest and the favicon — and never once shown to anyone
// actually using the app. It belongs beside the name in the header and above the
// name on the way in.
func TestMarkAppearsOnBothLayouts(t *testing.T) {
	h := newHarness(t)

	t.Run("header, on every signed-in page", func(t *testing.T) {
		body := h.get("/", h.ownerSession).Body.String()
		if !strings.Contains(body, `class="brand-mark"`) {
			t.Error("no mark beside the name in the header")
		}
		if !strings.Contains(body, "/static/icon.svg") {
			t.Error("the header mark does not come from the app's own icon")
		}
	})

	t.Run("sign-in page", func(t *testing.T) {
		body := h.get("/login", "").Body.String()
		if !strings.Contains(body, `class="brand-lockup"`) {
			t.Error("no mark on the sign-in page")
		}
	})

	// Decorative in both places: the product name is rendered as text right
	// next to it, so a screen reader announcing the image as well would only
	// repeat itself.
	t.Run("the mark is not announced twice", func(t *testing.T) {
		for _, page := range []struct{ path, session string }{
			{"/", h.ownerSession},
			{"/login", ""},
		} {
			body := h.get(page.path, page.session).Body.String()
			for _, chunk := range strings.Split(body, "<img")[1:] {
				tag := chunk[:strings.Index(chunk, ">")]
				if strings.Contains(tag, "icon.svg") && !strings.Contains(tag, `alt=""`) {
					t.Errorf("%s: the mark has alt text next to the name it repeats", page.path)
				}
			}
		}
	})
}
