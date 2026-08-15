package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// claimCanaries are strings that betray claim state. None may appear in a
// response rendered for the owner of the list in question.
//
// Display names are deliberately not on this list: the owner legitimately sees
// every family member's name in the share picker and the admin table. What can
// never legitimately appear is a claim's identity, a claimer-authored note, or
// any claim-state affordance.
func claimCanaries(h *harness) map[string]string {
	c := map[string]string{
		canaryNote:               "a claimer's private note",
		"still needed":           "remaining-quantity counter",
		"Fully claimed":          "fully-claimed marker",
		"Mark bought":            "claim state control",
		"Release":                "claim release control",
		"I&rsquo;ll get this":    "claim button",
		"/claims/":               "a claim-scoped URL",
		"cannot see any of this": "the claim list footnote",
	}
	if h.claim != nil {
		c[h.claim.ID] = "a claim ID"
	}
	return c
}

// TestOwnerBlindnessAcrossAllRoutes is the second test that gates P1 (plan §8).
//
// It is driven from the router's own registered route list rather than a
// hand-maintained one, so a new endpoint is covered the day it is added rather
// than the day someone remembers it.
func TestOwnerBlindnessAcrossAllRoutes(t *testing.T) {
	routes := walkRoutes(t)
	if len(routes) < 15 {
		t.Fatalf("route walk found only %d routes; the walk is probably broken", len(routes))
	}

	for _, rt := range routes {
		rt := rt
		t.Run(rt.method+" "+rt.pattern, func(t *testing.T) {
			// A fresh harness per route: several routes mutate state, and one
			// test's DELETE must not hollow out the next test's fixtures.
			h := newHarness(t)
			h.addClaims()

			target := h.fill(rt.pattern)
			var rec = h.request(rt.method, target, h.ownerSession, url.Values{})
			body := rec.Body.String()

			// Redirects and errors are fine; what matters is that no response
			// body carries claim state.
			for canary, what := range claimCanaries(h) {
				if strings.Contains(body, canary) {
					t.Errorf("owner-visible response for %s %s leaks %s (%q)",
						rt.method, target, what, canary)
				}
			}
			if rec.Code >= 500 {
				t.Errorf("%s %s returned %d for the owner", rt.method, target, rec.Code)
			}
		})
	}
}

// TestOwnerResponsesUnchangedByClaims closes the subtler leak vectors in the
// plan §3.2 table: ordering shifts, counters, and response-size differences.
// The same owner requests are compared byte for byte before and after claims
// exist.
func TestOwnerResponsesUnchangedByClaims(t *testing.T) {
	h := newHarness(t)

	paths := []string{
		"/",
		"/lists/" + h.list.ID,
		"/items/" + h.item.ID + "/edit",
		"/claims",
		"/admin",
	}

	before := map[string]string{}
	for _, p := range paths {
		rec := h.get(p, h.ownerSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s before claims: status %d", p, rec.Code)
		}
		before[p] = rec.Body.String()
	}

	h.addClaims()

	for _, p := range paths {
		rec := h.get(p, h.ownerSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s after claims: status %d", p, rec.Code)
		}
		after := rec.Body.String()
		if after != before[p] {
			t.Errorf("GET %s changed once the item was claimed (%d bytes -> %d bytes); "+
				"the owner can infer claim state from the difference",
				p, len(before[p]), len(after))
		}
	}
}

// TestOwnerCannotReachClaimEndpoints checks the mutation side of plan §3.2
// point 3.
func TestOwnerCannotReachClaimEndpoints(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	rec := h.post("/items/"+h.item.ID+"/claims", h.ownerSession, url.Values{"qty": {"1"}})
	if rec.Code != http.StatusNotFound {
		t.Errorf("owner claiming own item: status %d, want 404", rec.Code)
	}

	// Releasing someone else's claim is not the owner's to do either.
	rec = h.post("/claims/"+h.claim.ID+"/release", h.ownerSession, url.Values{})
	if rec.Code != http.StatusNotFound {
		t.Errorf("owner releasing a claim: status %d, want 404", rec.Code)
	}
	if got, err := h.st.ItemByID(t.Context(), h.item.ID); err != nil {
		t.Fatal(err)
	} else if got.ClaimedQty != 2 {
		t.Errorf("claim was disturbed by the owner: claimed_qty = %d, want 2", got.ClaimedQty)
	}
}

// TestClaimerSeesClaimState is the counterpart: the canaries must actually be
// reachable for a non-owner, otherwise the blindness test above would pass
// against an app that simply renders nothing.
func TestClaimerSeesClaimState(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	body := h.get("/lists/"+h.list.ID, h.claimerSession).Body.String()
	for _, want := range []string{"Release", "cannot see any of this"} {
		if !strings.Contains(body, want) {
			t.Errorf("claimer's list view is missing %q; the blindness test may be vacuous", want)
		}
	}
	if !strings.Contains(h.get("/claims", h.claimerSession).Body.String(), canaryNote) {
		t.Error("claimer cannot see their own note")
	}
}

type route struct {
	method  string
	pattern string
}

// walkRoutes enumerates every route the router has registered.
func walkRoutes(t *testing.T) []route {
	t.Helper()
	h := newHarness(t)

	var routes []route
	err := chi.Walk(h.srv.Router(),
		func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			pattern = strings.TrimSuffix(pattern, "/*")
			if strings.HasPrefix(pattern, "/static") {
				return nil
			}
			routes = append(routes, route{method: method, pattern: pattern})
			return nil
		})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	return routes
}

// fill substitutes real identifiers into a route pattern.
func (h *harness) fill(pattern string) string {
	r := strings.NewReplacer(
		"{listID}", h.list.ID,
		"{itemID}", h.item.ID,
		"{claimID}", claimIDOrPlaceholder(h),
		"{userID}", h.claimer.ID,
		"{sha}", h.sha,
		"{token}", "not-a-real-invite",
		"{tokenHash}", "not-a-real-hash",
	)
	return r.Replace(pattern)
}

func claimIDOrPlaceholder(h *harness) string {
	if h.claim != nil {
		return h.claim.ID
	}
	return "no-claim"
}
