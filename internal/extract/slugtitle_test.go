package extract

import "testing"

func TestTitleFromURLReadsTheNameOutOfTheAddress(t *testing.T) {
	cases := []struct{ name, url, want string }{
		{
			name: "product slug then id",
			url:  "https://shop.example.com/p/SOMEBRAND-Cordless-Ratchet-Tool-Only-ABC123B/318631225",
			want: "SOMEBRAND Cordless Ratchet Tool Only ABC123B",
		},
		{
			name: "shorter slug, different product path",
			url:  "https://shop.example.com/pd/SOMEBRAND-20V-Brushless-Grinder-Bare/5015742663",
			want: "SOMEBRAND 20V Brushless Grinder Bare",
		},
		{
			name: "slug is not the last segment",
			url:  "https://shop.example.com/ip/Cordless-Drill-Driver-Kit/12345",
			want: "Cordless Drill Driver Kit",
		},
		{
			name: "trailing marker segments",
			url:  "https://shop.example.com/p/lego-technic-set/-/A-79294464",
			want: "lego technic set",
		},
		{
			name: "storefront products path",
			url:  "https://shop.example.com/products/handmade-ceramic-mug",
			want: "handmade ceramic mug",
		},
		{
			name: "html suffix is dropped",
			url:  "https://shop.example.com/item/wool-winter-scarf.html",
			want: "wool winter scarf",
		},
		{
			name: "casing is left exactly as the shop wrote it",
			url:  "https://shop.example.com/p/DEWALT-ATOMIC-20V-MAX-Cordless/1",
			want: "DEWALT ATOMIC 20V MAX Cordless",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TitleFromURL(c.url); got != c.want {
				t.Errorf("TitleFromURL(%q)\n got %q\nwant %q", c.url, got, c.want)
			}
		})
	}
}

// Silence is the default. A wrong guess is worse than none, because it is the
// title somebody saves without reading it.
func TestTitleFromURLStaysQuietWhenThereIsNoName(t *testing.T) {
	cases := []struct{ name, url string }{
		{"canonical amazon path", "https://www.amazon.com/dp/B08N5WRWNW"},
		{"short link", "https://a.co/d/abc123"},
		{"bare numeric id", "https://shop.example.com/p/318631225"},
		{"single word segment", "https://shop.example.com/products/mug"},
		{"node identifier", "https://shop.example.com/b/BRAND/N-5yc1vZm5d"},
		{"uuid", "https://shop.example.com/i/6f1c3b2a-9d4e-4c1f-8a2b-7e5d0c9a1b3f"},
		{"hex blob", "https://shop.example.com/x/a1b2c3d4-e5f6"},
		{"percent encoded query text in path", "https://shop.example.com/s/two%20words%20here"},
		{"bare host", "https://shop.example.com"},
		{"root", "https://shop.example.com/"},
		{"not a url", "::::"},
		{"empty", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TitleFromURL(c.url); got != "" {
				t.Errorf("TitleFromURL(%q) = %q, want no suggestion", c.url, got)
			}
		})
	}
}

// Numbers separated by hyphens are left separated. "1-2-in" is a fraction and
// "2-0-Ah" is a decimal; the slug does not say which, so neither is guessed.
func TestTitleFromURLDoesNotInventPunctuationInNumbers(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"https://shop.example.com/p/Cordless-1-2-in-Ratchet-Tool/1", "Cordless 1 2 in Ratchet Tool"},
		{"https://shop.example.com/p/Scrubber-Kit-with-2-0-Ah-Battery/2", "Scrubber Kit with 2 0 Ah Battery"},
	} {
		if got := TitleFromURL(c.url); got != c.want {
			t.Errorf("TitleFromURL(%q)\n got %q\nwant %q", c.url, got, c.want)
		}
	}
}

func TestTitleFromURLRespectsTheTitleLimit(t *testing.T) {
	long := "https://shop.example.com/p/"
	for i := 0; i < 60; i++ {
		long += "word-"
	}
	long += "end/1"

	got := TitleFromURL(long)
	if got == "" {
		t.Fatal("a long slug produced nothing")
	}
	if len(got) > MaxSlugTitle {
		t.Errorf("title is %d bytes, over the %d the form accepts", len(got), MaxSlugTitle)
	}
	if got[len(got)-1] == ' ' {
		t.Error("title was cut leaving a trailing space")
	}
}

// The two features must not disagree: an address that is a listing has no
// product name in it, and offering the search terms as a title would be worse
// than offering nothing.
func TestNoTitleSuggestedForAddressesThatAreNotProducts(t *testing.T) {
	for _, raw := range []string{
		"https://www.homedepot.com/s/two-words-here",
		"https://www.amazon.com/s?k=cordless-drill",
		"https://shop.example.com/search/red-winter-coat",
		"https://shop.example.com/cart/some-thing-here",
	} {
		if shape := ClassifyNonProduct(raw); shape == "" {
			t.Errorf("ClassifyNonProduct(%q) found no shape; this test is not covering what it claims", raw)
		}
	}
}
