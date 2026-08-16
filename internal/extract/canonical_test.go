package extract_test

import (
	"context"
	"strings"
	"testing"

	"wishd/internal/extract"
	"wishd/internal/model"
)

// TestCanonicalNamesAnotherProduct is the case a real deployment hit: a
// marketplace page that 200s at the listing it was asked for and declares a
// different listing canonical. There is no redirect, so the guard fires on the
// canonical alone — and the alternative address is worth offering, not
// following.
//
// The hostname here is not decoration: NormalizeURL special-cases this
// marketplace's product paths, and this exercises that path.
func TestCanonicalNamesAnotherProduct(t *testing.T) {
	const requested = "https://www.amazon.com/dp/B0EXAMPLE1"
	page := loadPage(t, "product_variant_canonical.html", requested, requested)

	res := metadataChain().Run(context.Background(), page)
	extract.ApplySoft404Guard(res, page)

	if !res.Suspect {
		t.Fatal("a canonical naming a different product must be flagged suspect")
	}
	if res.LinkStatus != model.LinkSuspect {
		t.Errorf("link status = %q, want suspect", res.LinkStatus)
	}
	joined := strings.Join(res.SuspectReason, " | ")
	if !strings.Contains(joined, "canonical") {
		t.Errorf("suspect reasons %q should name the canonical mismatch", joined)
	}
	if strings.Contains(joined, "redirected") {
		t.Errorf("nothing redirected; reasons should not say so: %q", joined)
	}

	// The page's own details survive for the owner to look at. The guard
	// withholds them from the form; it does not throw them away.
	if res.Title == "" || res.PriceCents == nil {
		t.Fatalf("extraction should still report what the page said: title=%q price=%v",
			res.Title, res.PriceCents)
	}

	alt := extract.CanonicalAlternative(res.Canonical, requested)
	if alt != "https://www.amazon.com/dp/B0EXAMPLE2" {
		t.Errorf("canonical alternative = %q, want the normalized sibling listing", alt)
	}
}

func TestCanonicalAlternative(t *testing.T) {
	cases := []struct {
		name      string
		canonical string
		fetched   string
		want      string
	}{
		{
			"a sibling listing is offered, normalized down to its identity",
			"https://www.amazon.com/Merino-Wool-Hiking-Socks/dp/B0EXAMPLE2/ref=sr_1_3?qid=1",
			"https://www.amazon.com/dp/B0EXAMPLE1",
			"https://www.amazon.com/dp/B0EXAMPLE2",
		},
		{
			"a relative canonical resolves against the page it came from",
			"/products/hiking-socks",
			"https://cedarpress.example/collections/sale/products/hiking-sock",
			"https://cedarpress.example/products/hiking-socks",
		},
		{
			"another site is never offered: the tag is written by the page",
			"https://evil.example/products/hiking-socks",
			"https://cedarpress.example/products/hiking-sock",
			"",
		},
		{
			"a protocol-relative canonical cannot smuggle in a new host",
			"//evil.example/products/hiking-socks",
			"https://cedarpress.example/products/hiking-sock",
			"",
		},
		{
			"nothing to offer when it normalizes back to the address fetched",
			"https://www.amazon.com/Merino-Wool-Hiking-Socks/dp/B0EXAMPLE1",
			"https://www.amazon.com/dp/B0EXAMPLE1",
			"",
		},
		{
			"a non-http scheme is not an address to look up",
			"javascript:alert(1)",
			"https://cedarpress.example/products/hiking-sock",
			"",
		},
		{
			"no canonical, nothing offered",
			"",
			"https://cedarpress.example/products/hiking-sock",
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extract.CanonicalAlternative(tc.canonical, tc.fetched); got != tc.want {
				t.Errorf("CanonicalAlternative(%q, %q) = %q, want %q",
					tc.canonical, tc.fetched, got, tc.want)
			}
		})
	}
}
