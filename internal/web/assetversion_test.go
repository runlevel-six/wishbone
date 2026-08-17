package web

import (
	"net/http"
	"strings"
	"testing"
)

// The release that replaced every icon in the app shipped them to a server that
// served them correctly and to clients that never asked. Three caches held the
// old files — the HTTP cache under an hour of max-age, the browser's own
// long-lived favicon store, and a service worker matching by URL — and a new
// build changed none of the URLs, so none of them let go. These tests pin the
// mechanism that fixed it.

// TestAssetURLsCarryTheBuildVersion: every /static/ reference in the chrome, on
// both layouts, has to move when a build ships.
func TestAssetURLsCarryTheBuildVersion(t *testing.T) {
	h := newHarness(t)
	h.cfg.Version = "v9.9.9"

	for _, page := range []struct{ name, path, session string }{
		{"authenticated layout", "/", h.ownerSession},
		{"bare layout", "/login", ""},
	} {
		t.Run(page.name, func(t *testing.T) {
			body := h.get(page.path, page.session).Body.String()

			for _, ref := range []string{
				"/static/app.css?v=v9.9.9",
				"/static/icon.svg?v=v9.9.9",
				"/static/manifest.webmanifest?v=v9.9.9",
				"/static/apple-touch-icon.png?v=v9.9.9",
			} {
				if !strings.Contains(body, ref) {
					t.Errorf("%s is referenced without the build version", ref)
				}
			}
			// The service worker reads the version off <html> and puts it in its
			// own script URL, which is the only way a new build replaces it.
			if !strings.Contains(body, `data-asset-version="v9.9.9"`) {
				t.Error("no asset version on <html>; the service worker cannot key its cache")
			}
		})
	}
}

// TestFaviconIsRenderable is a regression guard with a specific cause behind it.
//
// Two brand changes shipped an SVG favicon that Gecko will not draw: with only a
// viewBox and no intrinsic width or height there is nothing for it to paint, so
// Firefox and its forks showed no icon at all — on desktop and on Android, since
// both are the same engine — and cached the failure so they stopped asking. The
// server was serving the file correctly the whole time, which is what made it
// hard to see.
func TestFaviconIsRenderable(t *testing.T) {
	h := newHarness(t)

	svg := h.get("/static/icon.svg", "").Body.String()
	if !strings.Contains(svg, `width="32"`) || !strings.Contains(svg, `height="32"`) {
		t.Error("icon.svg has no intrinsic size; Gecko renders nothing and remembers it")
	}

	// PNG fallbacks, so the icon does not depend on any one engine's SVG support.
	body := h.get("/login", "").Body.String()
	for _, want := range []string{
		`type="image/png" sizes="32x32"`,
		`type="image/png" sizes="192x192"`,
		`type="image/svg+xml"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the head does not offer %s", want)
		}
	}
	if code := h.get("/static/favicon-32.png", "").Code; code != http.StatusOK {
		t.Errorf("favicon-32.png = %d, want 200", code)
	}
}

// TestUnversionedAssetsStillWork: the version is a cache-busting token, not an
// access check. A hand-typed URL or an older page still open in a tab must not
// break.
func TestUnversionedAssetsStillWork(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/static/app.css", "/static/icon.svg", "/static/icon-192.png"} {
		if code := h.get(path, "").Code; code != http.StatusOK {
			t.Errorf("%s without a version = %d, want 200", path, code)
		}
	}
}

// TestVersionedAssetsAreImmutable: a versioned URL can be kept forever because
// it can never be the wrong file; an unversioned one gets an hour, so a release
// still reaches it.
func TestVersionedAssetsCacheForever(t *testing.T) {
	h := newHarness(t)

	versioned := h.get("/static/app.css?v=v9.9.9", "").Header().Get("Cache-Control")
	if !strings.Contains(versioned, "immutable") {
		t.Errorf("versioned asset Cache-Control = %q, want immutable", versioned)
	}

	bare := h.get("/static/app.css", "").Header().Get("Cache-Control")
	if strings.Contains(bare, "immutable") {
		t.Errorf("unversioned asset Cache-Control = %q; that file can change under the URL", bare)
	}
	if !strings.Contains(bare, "max-age=3600") {
		t.Errorf("unversioned asset Cache-Control = %q, want an hour", bare)
	}
}

// TestServiceWorkerKeysItsCacheOnTheVersion: the worker's own bytes do not
// change between releases, so its cache name has to come from the URL it was
// registered with. A hardcoded constant is a thing somebody has to remember to
// bump, and the icon release is the proof that nobody does.
func TestServiceWorkerKeysItsCacheOnTheVersion(t *testing.T) {
	h := newHarness(t)
	body := h.get("/sw.js", "").Body.String()

	if !strings.Contains(body, `searchParams.get("v")`) {
		t.Error("sw.js does not read a version from its own URL")
	}
	if !strings.Contains(body, `"wishbone-static-" + VERSION`) {
		t.Error("sw.js does not key its cache on that version")
	}
	// Icons in the precache list are what pinned a stale set into every
	// installed client; they are not needed for the shell to render.
	if strings.Contains(body, `const SHELL = [`) &&
		strings.Contains(body[strings.Index(body, "const SHELL = ["):], "icon") {
		t.Error("icons are back in the precache list")
	}
}
