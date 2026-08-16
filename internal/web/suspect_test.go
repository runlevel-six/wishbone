package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wishd/internal/model"
	"wishd/internal/web/templates"
)

// suspectFound is the set of hidden fields the "This link looks wrong" warning
// carries, as the browser would post them back.
func suspectFound() url.Values {
	return url.Values{
		"found_title":       {"Merino Wool Hiking Socks, Cushioned"},
		"found_description": {"Full-cushion merino hiking sock."},
		"found_price":       {"26.00"},
		"found_currency":    {"usd"},
		"found_image_url":   {"https://m.media-amazon.example/images/I/merino-socks.jpg"},
		"found_url":         {"https://www.amazon.com/dp/B0EXAMPLE1"},
		"found_url_raw":     {"https://www.amazon.com/dp/B0EXAMPLE1"},
		"found_link_status": {"suspect"},
		"found_attrs":       {`{"size":"L"}`},
		"found_sources":     {`{"title":"jsonld","price":"jsonld"}`},
	}
}

// TestAcceptSuspectPreviewAppliesWhatWasShown: the guard holds a lookup back,
// the owner looks at it and says use it. What lands in the form has to be
// exactly what the warning displayed.
func TestAcceptSuspectPreviewAppliesWhatWasShown(t *testing.T) {
	h := newHarnessOpt(t, true)

	rec := h.post("/lists/"+h.list.ID+"/items/preview/accept", h.ownerSession, suspectFound())
	if rec.Code != http.StatusOK {
		t.Fatalf("accept returned %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		`value="Merino Wool Hiking Socks, Cushioned"`,
		`value="26.00"`,
		`value="USD"`, // normalized on the way in
		`https://www.amazon.com/dp/B0EXAMPLE1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("accepted form is missing %q", want)
		}
	}
	// The form says where the values came from, because "unsure but you asked
	// for it" is a different claim from "filled in from the page".
	if !strings.Contains(body, "because you asked") {
		t.Error("accepted form does not say the details came from a page Wishbone doubted")
	}
	if strings.Contains(body, "Filled in from the page.") {
		t.Error("an accepted suspect result must not read as a clean lookup")
	}
	// And the item still carries the mark: nothing about the page improved.
	if !strings.Contains(body, `name="link_status" value="suspect"`) {
		t.Error("accepted form does not carry the suspect link status")
	}

	// A page that answered with an error stays dead, and accepting cannot
	// promote a held-back result to a clean one however the field arrives.
	for status, want := range map[string]string{
		"dead":    "dead",
		"ok":      "suspect",
		"":        "suspect",
		"perfect": "suspect",
	} {
		form := suspectFound()
		form.Set("found_link_status", status)
		got := h.post("/lists/"+h.list.ID+"/items/preview/accept", h.ownerSession, form).Body.String()
		if !strings.Contains(got, `name="link_status" value="`+want+`"`) {
			t.Errorf("accepting a %q lookup should record %q", status, want)
		}
	}
}

// TestFoundValuesSurviveTheRoundTrip covers the carriers themselves. The
// warning has to hand every field back intact, and hand back nothing at all
// rather than something half-parsed.
func TestFoundValuesSurviveTheRoundTrip(t *testing.T) {
	attrs := map[string]string{"size": "L", "color": "charcoal"}
	if got := decodeStringMap(encodeStringMap(attrs)); len(got) != 2 || got["size"] != "L" || got["color"] != "charcoal" {
		t.Errorf("attributes did not survive the round trip: %v", got)
	}
	if got := encodeStringMap(nil); got != "{}" {
		t.Errorf("empty map encoded as %q, want {}", got)
	}
	for _, bad := range []string{"", "not json", `["a"]`, `{"size":1}`} {
		if got := decodeStringMap(bad); len(got) != 0 {
			t.Errorf("decodeStringMap(%q) = %v, want empty", bad, got)
		}
	}

	// Oversized values prefill a form that can still be saved rather than one
	// that fails validation on the way out.
	if got := clip(strings.Repeat("a", 300), 200); len([]rune(got)) != 200 {
		t.Errorf("clip left %d runes, want 200", len([]rune(got)))
	}
	if got := clip("  spaced  ", 200); got != "spaced" {
		t.Errorf("clip(%q) = %q", "  spaced  ", got)
	}
	// Cutting mid-rune would put a replacement character in the form.
	if got := clip(strings.Repeat("é", 10), 5); got != strings.Repeat("é", 5) {
		t.Errorf("clip split a multi-byte character: %q", got)
	}
}

// TestSuspectWarningOffersBothChoices renders the warning directly: the
// handler that produces it needs a live fetch, and no test in this repo
// touches the network.
func TestSuspectWarningOffersBothChoices(t *testing.T) {
	f := templates.ItemFormData{
		ListID:        "list-1",
		Quantity:      1,
		Suspect:       true,
		Extracted:     true,
		SuspectReason: []string{"the page says its canonical address is /Hiking-Socks/dp/B0EXAMPLE2"},
		Found: &templates.FoundDetails{
			Title:     "Merino Wool Hiking Socks, Cushioned",
			Price:     "26.00",
			Currency:  "USD",
			Descr:     "Full-cushion merino hiking sock.",
			ImageURL:  "https://m.media-amazon.example/images/I/merino-socks.jpg",
			URL:       "https://www.amazon.com/dp/B0EXAMPLE1",
			URLRaw:    "https://www.amazon.com/dp/B0EXAMPLE1",
			Canonical: "https://www.amazon.com/dp/B0EXAMPLE2",
		},
	}
	var buf strings.Builder
	if err := templates.ItemFormBody(templates.Page{}, f).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := buf.String()

	for _, want := range []string{
		"This link looks wrong",
		"Merino Wool Hiking Socks, Cushioned", // shown, so the owner can judge it
		"Use these details",
		"/lists/list-1/items/preview/accept",
		"Look up https://www.amazon.com/dp/B0EXAMPLE2 instead",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("suspect warning is missing %q", want)
		}
	}
	// The canonical goes back through the ordinary lookup, as a URL the owner
	// chose — not as data lifted off the page automatically.
	if !strings.Contains(body, `hx-vals="{&#34;url_raw&#34;:&#34;https://www.amazon.com/dp/B0EXAMPLE2&#34;}"`) {
		t.Error("the canonical button does not post the canonical URL to the lookup")
	}
	// Nothing is applied: the real fields stay empty until a button is clicked.
	if strings.Contains(body, `name="title" value="Merino`) {
		t.Error("a suspect lookup prefilled the form")
	}
	if strings.Contains(body, `name="image_url"`) {
		t.Error("a suspect lookup queued the retailer's image for saving")
	}

	// With nothing read off the page there is nothing to offer, and the
	// warning falls back to what it always said.
	f.Found = nil
	buf.Reset()
	if err := templates.ItemFormBody(templates.Page{}, f).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if plain := buf.String(); strings.Contains(plain, "Use these details") ||
		!strings.Contains(plain, "enter the details") {
		t.Error("with nothing found, the warning should stay the plain one")
	}
}

// TestRefusedLookupBlamesTheShopNotTheLink: a retailer answering 403 is a fact
// about the retailer. The person pasted a good link, and the form has to say
// so — the previous wording sent them off to check something that was fine.
func TestRefusedLookupBlamesTheShopNotTheLink(t *testing.T) {
	f := templates.ItemFormData{
		ListID:        "list-1",
		Quantity:      1,
		Extracted:     true,
		Blocked:       true,
		BlockedStatus: 403,
		URLRaw:        "https://www.deptstore.example/p/a-dress-shirt/DS-2201.html",
		// Whatever a lookup concluded, a refused one concluded nothing.
		LinkStatus: model.LinkUnknown,
	}
	var buf strings.Builder
	if err := templates.ItemFormBody(templates.Page{}, f).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := buf.String()

	if !strings.Contains(body, "would not let Wishbone read the page") || !strings.Contains(body, "403") {
		t.Error("a refused lookup does not say the shop refused, with its status")
	}
	if strings.Contains(body, "This link looks wrong") {
		t.Error("a refused lookup is reported as a bad link")
	}
	if strings.Contains(body, "Filled in from the page.") ||
		strings.Contains(body, "did not publish any product details") {
		t.Error("a refused lookup is also described as an ordinary lookup")
	}
	// Nothing is stored against the link, so the list must not warn about it.
	if !strings.Contains(body, `name="link_status" value="unknown"`) {
		t.Error("a refused lookup did not leave the link status alone")
	}
	if strings.Contains(body, `name="link_status" value="dead"`) {
		t.Error("a refused lookup marked the link dead")
	}
}

// TestSuspectWarningNeverRendersTheRetailersImage: Wishbone fetches images
// itself so the shop never learns who is looking. An <img> pointed at the
// retailer from the add-item form would hand over exactly that.
func TestSuspectWarningNeverRendersTheRetailersImage(t *testing.T) {
	h := newHarnessOpt(t, true)

	body := h.post("/lists/"+h.list.ID+"/items/preview/accept", h.ownerSession, suspectFound()).Body.String()
	for _, frag := range []string{
		`<img src="https://m.media-amazon.example`,
		`<img class="thumb" src="https://m.media-amazon.example`,
	} {
		if strings.Contains(body, frag) {
			t.Errorf("the form loads a remote image: %q", frag)
		}
	}
}

// TestAcceptSuspectPreviewIsOwnerOnly: it writes nothing, but it renders a
// form for someone else's list, and every item route is owner-gated.
func TestAcceptSuspectPreviewIsOwnerOnly(t *testing.T) {
	h := newHarnessOpt(t, true)

	for _, session := range []string{h.strangerSession, h.claimerSession} {
		rec := h.post("/lists/"+h.list.ID+"/items/preview/accept", session, suspectFound())
		if rec.Code != http.StatusNotFound {
			t.Errorf("non-owner accept returned %d, want 404", rec.Code)
		}
	}
	rec := h.post("/lists/"+h.list.ID+"/items/preview/accept", "", suspectFound())
	if rec.Code == http.StatusOK {
		t.Error("accept served an unauthenticated request")
	}
}

// TestCreatedItemKeepsWhatTheLookupConcluded: an item added from an accepted
// suspect lookup is stored suspect, so the list keeps warning about it.
func TestCreatedItemKeepsWhatTheLookupConcluded(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		name       string
		linkStatus string
		rawURL     string
		want       string
	}{
		{"an accepted suspect lookup stays suspect", "suspect", "https://shop.example/skillet", model.LinkSuspect},
		{"a clean lookup is recorded as such", "ok", "https://shop.example/skillet", model.LinkOK},
		{"a status with no link behind it means nothing", "ok", "", model.LinkUnknown},
		{"anything else falls back to unknown", "sparkling", "https://shop.example/skillet", model.LinkUnknown},
		{"a hand-typed item is unknown, as it always was", "", "https://shop.example/skillet", model.LinkUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{
				"title":       {"Cast iron skillet"},
				"quantity":    {"1"},
				"url_raw":     {tc.rawURL},
				"link_status": {tc.linkStatus},
			}
			rec := h.post("/lists/"+h.list.ID+"/items", h.ownerSession, form)
			if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
				t.Fatalf("create returned %d: %s", rec.Code, rec.Body.String())
			}
			it := h.newestItem()
			if it.LinkStatus != tc.want {
				t.Errorf("link status = %q, want %q", it.LinkStatus, tc.want)
			}
		})
	}
}

// TestSuspectItemWarnsOnTheList closes the loop: the status the accept path
// stores is the one the owner sees on the list.
func TestSuspectItemWarnsOnTheList(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if err := h.st.SetLinkStatus(ctx, h.item.ID, model.LinkSuspect, model.TimeString(model.Now())); err != nil {
		t.Fatalf("set link status: %v", err)
	}
	body := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String()
	if !strings.Contains(body, "may no longer point at the right product") {
		t.Error("a suspect item does not warn the owner on the list")
	}
}

// newestItem returns the most recently created item on the harness list.
func (h *harness) newestItem() *model.Item {
	h.t.Helper()
	items, err := h.st.LiveItemsForList(context.Background(), h.list.ID)
	if err != nil {
		h.t.Fatalf("items: %v", err)
	}
	if len(items) == 0 {
		h.t.Fatal("no items on the list")
	}
	newest := items[0]
	for _, it := range items {
		if it.CreatedAt > newest.CreatedAt || (it.CreatedAt == newest.CreatedAt && it.SortOrder > newest.SortOrder) {
			newest = it
		}
	}
	return newest
}
