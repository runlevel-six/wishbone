package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wishd/internal/model"
)

// TestSafeNextRejectsOffsiteRedirects guards a genuine phishing primitive: a
// link to the real host that bounces to an attacker's sign-in page after
// authenticating.
func TestSafeNextRejectsOffsiteRedirects(t *testing.T) {
	hostile := []string{
		"https://evil.example/",
		"//evil.example/",
		"/\\evil.example",
		"\\\\evil.example",
		"http://evil.example",
		"javascript:alert(1)",
		"  //evil.example",
	}
	for _, in := range hostile {
		if got := safeNext(in); got != "" {
			t.Errorf("safeNext(%q) = %q, want it refused", in, got)
		}
	}

	allowed := map[string]string{
		"/":                    "/",
		"/lists/abc/items/new": "/lists/abc/items/new",
		"/share-target?url=https%3A%2F%2Fshop.example%2Fx": "/share-target?url=https%3A%2F%2Fshop.example%2Fx",
	}
	for in, want := range allowed {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSharedURLExtraction covers what phones actually send. Android apps often
// put the link inside the text field rather than the url field.
func TestSharedURLExtraction(t *testing.T) {
	cases := []struct {
		name string
		q    url.Values
		want string
	}{
		{"url field", url.Values{"url": {"https://shop.example/thing"}}, "https://shop.example/thing"},
		{"link inside text", url.Values{"text": {"Cast iron skillet https://shop.example/skillet"}}, "https://shop.example/skillet"},
		{"url field wins", url.Values{
			"url":  {"https://shop.example/right"},
			"text": {"https://shop.example/wrong"},
		}, "https://shop.example/right"},
		{"trailing punctuation trimmed", url.Values{"text": {"look at this https://shop.example/x."}}, "https://shop.example/x"},
		{"link inside title", url.Values{"title": {"https://shop.example/from-title"}}, "https://shop.example/from-title"},
		{"nothing shareable", url.Values{"text": {"just some words"}}, ""},
		{"empty", url.Values{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sharedURL(tc.q); got != tc.want {
				t.Errorf("sharedURL(%v) = %q, want %q", tc.q, got, tc.want)
			}
		})
	}
}

// TestShareTargetRouting covers the three shapes: no list, one list, several.
func TestShareTargetRouting(t *testing.T) {
	shared := "https://shop.example/skillet"
	target := "/share-target?url=" + url.QueryEscape(shared)

	t.Run("one list goes straight to the form", func(t *testing.T) {
		h := newHarness(t)
		rec := h.get(target, h.ownerSession)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status %d, want 303", rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.HasPrefix(loc, "/lists/"+h.list.ID+"/items/new?url=") {
			t.Errorf("Location = %q, want the add-item form for the only list", loc)
		}
		if !strings.Contains(loc, url.QueryEscape(shared)) {
			t.Errorf("Location = %q, want it to carry the shared link", loc)
		}
	})

	t.Run("several lists ask which", func(t *testing.T) {
		h := newHarness(t)
		second := &model.List{OwnerID: h.owner.ID, Name: "Birthday", Visibility: model.VisibilityPrivate}
		if err := h.st.CreateList(t.Context(), second); err != nil {
			t.Fatal(err)
		}
		rec := h.get(target, h.ownerSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{"Add to which list?", h.list.ID, second.ID} {
			if !strings.Contains(body, want) {
				t.Errorf("picker missing %q", want)
			}
		}
	})

	t.Run("no lists says so instead of losing the link", func(t *testing.T) {
		h := newHarness(t)
		rec := h.get(target, h.strangerSession) // owns nothing
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status %d, want 303", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("Location = %q, want /", loc)
		}
	})
}

// TestShareTargetRequiresAuthAndReturns is the expired-session case: a link
// shared from a phone must survive sign-in rather than dumping the person on
// the dashboard.
func TestShareTargetRequiresAuthAndReturns(t *testing.T) {
	h := newHarness(t)
	target := "/share-target?url=" + url.QueryEscape("https://shop.example/skillet")

	rec := h.get(target, "") // no session
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want a redirect to sign in", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("Location = %q, want /login?next=…", loc)
	}
	next, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.Query().Get("next"); got != target {
		t.Errorf("next = %q, want %q", got, target)
	}

	// And the sign-in page must carry it forward in the form.
	body := h.get(loc, "").Body.String()
	if !strings.Contains(body, `name="next"`) {
		t.Error("sign-in form does not carry the next field")
	}
}

// TestAddItemFormAutoRunsSharedLookup: arriving from a share sheet should not
// require tapping "Look it up".
func TestAddItemFormAutoRunsSharedLookup(t *testing.T) {
	h := newHarness(t)

	plain := h.get("/lists/"+h.list.ID+"/items/new", h.ownerSession).Body.String()
	if strings.Contains(plain, `hx-trigger="load, submit"`) {
		t.Error("a normal add-item page should not fire a lookup on load")
	}

	shared := h.get("/lists/"+h.list.ID+"/items/new?url="+
		url.QueryEscape("https://shop.example/skillet"), h.ownerSession).Body.String()
	if !strings.Contains(shared, "https://shop.example/skillet") {
		t.Error("the shared link was not prefilled")
	}
	// Fetching is disabled in the default harness, so the trigger stays off:
	// no point auto-running a lookup that cannot run.
	if strings.Contains(shared, `hx-trigger="load, submit"`) {
		t.Error("auto-lookup fired with fetching disabled")
	}

	// With fetching on, the whole point is that the lookup runs without being
	// asked — that is what makes sharing from a phone worth doing.
	hf := newHarnessOpt(t, true)
	withFetch := hf.get("/lists/"+hf.list.ID+"/items/new?url="+
		url.QueryEscape("https://shop.example/skillet"), hf.ownerSession).Body.String()
	if !strings.Contains(withFetch, `hx-trigger="load, submit"`) {
		t.Error("a shared link did not auto-run the lookup")
	}
	if !strings.Contains(hf.get("/lists/"+hf.list.ID+"/items/new", hf.ownerSession).Body.String(),
		`hx-trigger="submit"`) {
		t.Error("a normal add-item page should keep the plain submit trigger")
	}
}

// TestManifestAndServiceWorker: the two files installability depends on.
func TestManifestAndServiceWorker(t *testing.T) {
	h := newHarness(t)

	man := h.get("/static/manifest.webmanifest", "")
	if man.Code != http.StatusOK {
		t.Fatalf("manifest status %d", man.Code)
	}
	if ct := man.Header().Get("Content-Type"); !strings.Contains(ct, "manifest+json") {
		t.Errorf("manifest Content-Type = %q, want application/manifest+json", ct)
	}
	for _, want := range []string{`"share_target"`, `/share-target`, `"maskable"`, `"display": "standalone"`} {
		if !strings.Contains(man.Body.String(), want) {
			t.Errorf("manifest missing %s", want)
		}
	}

	sw := h.get("/sw.js", "")
	if sw.Code != http.StatusOK {
		t.Fatalf("sw.js status %d — a service worker outside the root cannot control the app", sw.Code)
	}
	if !strings.Contains(sw.Body.String(), "/static/") {
		t.Error("sw.js does not look like the expected worker")
	}
}
