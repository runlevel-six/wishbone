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
	})

	t.Run("sign-in page", func(t *testing.T) {
		body := h.get("/login", "").Body.String()
		if !strings.Contains(body, `class="brand-lockup"`) {
			t.Error("no mark on the sign-in page")
		}
		if !strings.Contains(body, `class="brand-lockup-mark"`) {
			t.Error("the lockup has no mark in it")
		}
	})

	// The mark is inline SVG rather than an <img> so that it can take the
	// reader's palette; an image's own pixels are not the page's to restyle.
	// Both halves of that have to hold: it is drawn inline, and its colors come
	// from the stylesheet rather than being baked into the markup.
	t.Run("the mark wears the theme", func(t *testing.T) {
		for _, page := range []struct{ path, session string }{
			{"/", h.ownerSession},
			{"/login", ""},
		} {
			body := h.get(page.path, page.session).Body.String()
			for _, want := range []string{`<svg class="brand`, `class="mark-plate"`, `class="mark-bow"`} {
				if !strings.Contains(body, want) {
					t.Errorf("%s: the mark is missing %s", page.path, want)
				}
			}
			// The favicon is a different job and stays green: a browser asks for
			// one before it knows who is looking.
			if !strings.Contains(body, "/static/icon.svg") {
				t.Errorf("%s: no favicon", page.path)
			}
		}
	})

	// Decorative in both places: the product name is rendered as text right
	// next to it, so a screen reader announcing the mark as well would only
	// repeat itself.
	t.Run("the mark is not announced twice", func(t *testing.T) {
		for _, page := range []struct{ path, session string }{
			{"/", h.ownerSession},
			{"/login", ""},
		} {
			body := h.get(page.path, page.session).Body.String()
			marks := 0
			for _, chunk := range strings.Split(body, "<svg")[1:] {
				tag := chunk[:strings.Index(chunk, ">")]
				marks++
				if !strings.Contains(tag, `aria-hidden="true"`) {
					t.Errorf("%s: the mark is announced next to the name it repeats", page.path)
				}
			}
			if marks == 0 {
				t.Errorf("%s: no inline mark to check", page.path)
			}
		}
	})
}
