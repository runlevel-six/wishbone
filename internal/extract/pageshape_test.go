package extract

import "testing"

// The addresses that prompted this file came out of the production log, so they
// are the cases the test is built from rather than invented ones.
func TestClassifyNonProductRecognizesListings(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want PageShape
	}{
		{
			// Pasted once with the typed search terms still misspelled, which is
			// the tell that it came from a browser bar after a search.
			name: "search with the terms in the path",
			url:  "https://www.homedepot.com/s/cordless%20sprayer%20with%20spare%20tank?NCNI-5=",
			want: ShapeSearch,
		},
		{
			// Pasted three times in seven minutes. Nothing was ever coming back.
			name: "brand listing",
			url:  "https://www.homedepot.com/b/SOMEBRAND/N-5yc1vZm5d",
			want: ShapeCategory,
		},
		{name: "amazon search", url: "https://www.amazon.com/s?k=cordless+drill", want: ShapeSearch},
		{name: "amazon browse node", url: "https://www.amazon.com/b?node=12345", want: ShapeCategory},
		{name: "amazon gp browse", url: "https://www.amazon.com/gp/browse?node=9", want: ShapeCategory},
		{name: "amazon country host", url: "https://www.amazon.co.uk/s?k=building+set", want: ShapeSearch},
		{name: "target search", url: "https://www.target.com/s?searchTerm=building+set", want: ShapeSearch},
		{name: "target category", url: "https://www.target.com/c/toys/-/N-5xtb6", want: ShapeCategory},
		{name: "walmart browse", url: "https://www.walmart.com/browse/tools/1234", want: ShapeCategory},
		{name: "lowes product list", url: "https://www.lowes.com/pl/Drills/4294857977", want: ShapeCategory},
		{name: "ebay search", url: "https://www.ebay.com/sch/i.html?_nkw=drill", want: ShapeSearch},

		// Spelled-out segments carry their meaning anywhere, including hosts the
		// table has never heard of.
		{name: "generic search path", url: "https://shop.example.com/search?q=mug", want: ShapeSearch},
		{name: "generic cart", url: "https://shop.example.com/cart", want: ShapeCart},
		{name: "generic checkout", url: "https://shop.example.com/checkout/step2", want: ShapeCart},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyNonProduct(c.url); got != c.want {
				t.Errorf("ClassifyNonProduct(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

// A wrong "that is not a product" is worse than the blank form it replaces: it
// tells somebody their good link is bad. These must all fall through.
func TestClassifyNonProductLetsProductsThrough(t *testing.T) {
	cases := []struct{ name, url string }{
		{
			// The product address from the same shop as the two listings above.
			// "p" is deliberately absent from that host's table.
			name: "home improvement product",
			url:  "https://www.homedepot.com/p/SOMEBRAND-18V-Cordless-Bug-Zapper-ABC123B/320011852",
		},
		{name: "amazon dp", url: "https://www.amazon.com/dp/B08N5WRWNW"},
		{name: "amazon slug and dp", url: "https://www.amazon.com/Some-Toy/dp/B08N5WRWNW"},
		{name: "amazon short link", url: "https://a.co/d/abc123"},
		{name: "amazon gp product", url: "https://www.amazon.com/gp/product/B08N5WRWNW"},
		{name: "target product", url: "https://www.target.com/p/building-set/-/A-79294464"},
		{name: "walmart product", url: "https://www.walmart.com/ip/Cordless-Drill/12345"},
		{name: "lowes product", url: "https://www.lowes.com/pd/SOMEBRAND-Drill/1000123456"},

		// A one-letter segment means something on the hosts that define it and
		// nothing anywhere else. Neither of these may be touched.
		{name: "unknown host s segment", url: "https://shop.example.com/s/some-product"},
		{name: "unknown host b segment", url: "https://blog.example.com/b/a-product-review"},

		// A bare host is not a product either, but the existing guard already
		// handles what a front page answers, and refusing here would be noise.
		{name: "bare host", url: "https://www.homedepot.com/"},
		{name: "bare host no slash", url: "https://www.homedepot.com"},

		{name: "not a url", url: "::::"},
		{name: "empty", url: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyNonProduct(c.url); got != "" {
				t.Errorf("ClassifyNonProduct(%q) = %q, want no shape", c.url, got)
			}
		})
	}
}

// The classifier runs on the normalized address, so what normalization produces
// has to keep matching. It trims trailing slashes, which is exactly the kind of
// change that would silently stop a prefix rule from firing.
func TestClassifyNonProductAfterNormalization(t *testing.T) {
	for _, raw := range []string{
		"https://www.homedepot.com/b/SOMEBRAND/N-5yc1vZm5d/",
		"HTTPS://WWW.HOMEDEPOT.COM/B/SOMEBRAND/N-5yc1vZm5d",
		"www.homedepot.com/s/drill",
	} {
		normalized, err := NormalizeURL(raw)
		if err != nil {
			t.Fatalf("NormalizeURL(%q): %v", raw, err)
		}
		if got := ClassifyNonProduct(normalized); got == "" {
			t.Errorf("ClassifyNonProduct(%q) found no shape after normalizing %q", normalized, raw)
		}
	}
}

// Every shape a person can be shown needs words for it; an empty label would
// render as a gap in the middle of a sentence.
func TestEveryShapeHasALabel(t *testing.T) {
	for _, s := range []PageShape{ShapeSearch, ShapeCategory, ShapeCart} {
		if s.Label() == "" {
			t.Errorf("PageShape %q has no label", s)
		}
	}
}
