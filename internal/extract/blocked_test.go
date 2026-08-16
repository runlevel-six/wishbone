package extract_test

import (
	"context"
	"strings"
	"testing"

	"wishd/internal/extract"
	"wishd/internal/model"
)

// TestRefusedRequestIsNotABadLink covers what some retailers actually do:
// answer 403 to the fetcher and to the sidecar, from an address whose browser
// opens the same URL fine. Reporting that as a dead or suspect link tells the
// person their link is broken when it is not.
func TestRefusedRequestIsNotABadLink(t *testing.T) {
	const requested = "https://www.deptstore.example/p/a-dress-shirt/DS-2201.html"

	for _, status := range []int{401, 403, 429, 451, 500, 503} {
		page := loadPage(t, "product_live.html", requested, requested)
		page.StatusCode = status

		res := metadataChain().Run(context.Background(), page)
		extract.ApplySoft404Guard(res, page)

		if !res.Blocked {
			t.Errorf("status %d: not marked blocked", status)
		}
		if res.BlockedStatus != status {
			t.Errorf("status %d: BlockedStatus = %d", status, res.BlockedStatus)
		}
		if res.Suspect {
			t.Errorf("status %d: marked suspect — the guard doubts pages, and this one said nothing", status)
		}
		if res.LinkStatus != model.LinkUnknown {
			t.Errorf("status %d: link status = %q, want unknown", status, res.LinkStatus)
		}
		// A block page carries meta tags of its own. An item titled "Access
		// Denied" priced at nothing is the same failure with a new cause.
		if res.Title != "" || res.PriceCents != nil || res.Description != "" || len(res.ImageURLs) > 0 {
			t.Errorf("status %d: scraped a block page: title=%q price=%v descr=%q images=%d",
				status, res.Title, res.PriceCents, res.Description, len(res.ImageURLs))
		}
		if len(res.Sources) != 0 {
			t.Errorf("status %d: field sources survived: %v", status, res.Sources)
		}
	}
}

// TestGoneIsStillDead: 404 and 410 are the shop saying the thing is not there,
// which *is* evidence about the link and must keep saying so.
func TestGoneIsStillDead(t *testing.T) {
	const requested = "https://cedarpress.example/products/the-childrens-classics-set"

	for _, status := range []int{404, 410} {
		page := loadPage(t, "product_live.html", requested, requested)
		page.StatusCode = status

		res := metadataChain().Run(context.Background(), page)
		extract.ApplySoft404Guard(res, page)

		if res.Blocked {
			t.Errorf("status %d: marked blocked; a missing page is not a refused one", status)
		}
		if !res.Suspect {
			t.Errorf("status %d: not marked suspect", status)
		}
		if res.LinkStatus != model.LinkDead {
			t.Errorf("status %d: link status = %q, want dead", status, res.LinkStatus)
		}
		if joined := strings.Join(res.SuspectReason, " | "); !strings.Contains(joined, "gone") {
			t.Errorf("status %d: reasons %q should say the page is gone", status, joined)
		}
	}
}
