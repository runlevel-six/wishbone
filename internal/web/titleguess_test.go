package web

import (
	"context"
	"strings"
	"testing"

	"wishbone/internal/extract"
	"wishbone/internal/web/templates"
)

// A page nothing could read leaves the person typing the product's name by
// hand. Shops write that name into the address, so it is offered — filled in,
// and labeled as the guess it is.
func TestGuessedTitleIsOfferedAndSaysSo(t *testing.T) {
	f := templates.ItemFormData{
		ListID:        "list-1",
		Quantity:      1,
		Extracted:     true,
		Blocked:       true,
		BlockedStatus: 403,
		// The normalized address, which is what the handler has in hand by the
		// time it gets here — set on the pre-fetch path and again from the
		// preview, so it is populated whether the fetch failed or was refused.
		URL: "https://www.toolshop.example/p/SOMEBRAND-Cordless-Ratchet-Tool-Only-ABC123B/318631225",
	}
	suggestTitleFromAddress(&f)

	if f.Title != "SOMEBRAND Cordless Ratchet Tool Only ABC123B" {
		t.Fatalf("title = %q, want it read out of the address", f.Title)
	}
	if !f.TitleGuessed {
		t.Error("the title was filled in without being marked a guess")
	}
	if f.Sources["title"] != extract.SourceURLSlug {
		t.Errorf(`Sources["title"] = %q, want %q — a later re-scrape has to know this was a guess`,
			f.Sources["title"], extract.SourceURLSlug)
	}
	// The field that turns a mistake into the wrong present is never guessed.
	if f.Price != "" {
		t.Errorf("a price was guessed: %q", f.Price)
	}

	var buf strings.Builder
	if err := templates.ItemFormBody(templates.Page{}, f).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := buf.String()

	if !strings.Contains(body, "Read out of the link itself") {
		t.Error("the form does not say the name is a guess read out of the link")
	}
	// With the name already there, the advice has to change: the price is what
	// is actually missing, and telling somebody to copy both wastes their time.
	if !strings.Contains(body, "the price is the one thing") {
		t.Error("a blocked page with a guessed name still asks for the name as well")
	}
}

func TestNoGuessedTitleWhenTheFormDoesNotNeedOne(t *testing.T) {
	url := "https://www.toolshop.example/p/SOMEBRAND-Cordless-Ratchet-Tool/1"

	t.Run("a real title is never overwritten", func(t *testing.T) {
		f := templates.ItemFormData{Title: "What the page actually said", URL: url}
		suggestTitleFromAddress(&f)
		if f.Title != "What the page actually said" {
			t.Errorf("title = %q, want the page's own", f.Title)
		}
		if f.TitleGuessed {
			t.Error("an extracted title was marked as guessed")
		}
	})

	t.Run("a suspect result is left alone", func(t *testing.T) {
		// The suspect flow already shows what the page said behind one button. A
		// second, worse candidate beside it only makes that choice harder.
		f := templates.ItemFormData{Suspect: true, URL: url}
		suggestTitleFromAddress(&f)
		if f.Title != "" || f.TitleGuessed {
			t.Errorf("a suspect result was given a guessed title: %q", f.Title)
		}
	})

	t.Run("an address with no name in it offers nothing", func(t *testing.T) {
		f := templates.ItemFormData{URL: "https://www.example.com/dp/B08N5WRWNW"}
		suggestTitleFromAddress(&f)
		if f.Title != "" || f.TitleGuessed {
			t.Errorf("title = %q, want nothing offered", f.Title)
		}
	})
}

// The form must not claim a lookup succeeded just because a field is populated.
// believedLookup is what the "Filled in from the page." line hangs off, and a
// guess did not come from the page.
func TestGuessedTitleIsNotReportedAsASuccessfulLookup(t *testing.T) {
	f := templates.ItemFormData{
		Quantity:      1,
		Extracted:     true,
		Blocked:       true,
		BlockedStatus: 403,
		URL:           "https://www.toolshop.example/p/SOMEBRAND-Cordless-Ratchet-Tool/1",
	}
	suggestTitleFromAddress(&f)

	var buf strings.Builder
	if err := templates.ItemFormBody(templates.Page{}, f).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(buf.String(), "Filled in from the page.") {
		t.Error("a guessed title is described as having been filled in from the page")
	}
}
