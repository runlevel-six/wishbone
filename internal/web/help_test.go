package web

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The help page (templates.Help) is the app explaining itself to the family
// rather than to whoever deploys it. These tests hold the three properties that
// make it worth having:
//
//  1. Everybody can reach it, including somebody who cannot get past sign-in.
//  2. Every link into it lands somewhere, and it names the controls as they are
//     actually labeled — a help page that has drifted from the interface is
//     worse than none, because it is believed.
//  3. It is the same page for every reader. That is what keeps it out of the
//     owner-blindness argument entirely: a page assembled from no data cannot
//     leak any.

func helpBody(t *testing.T, h *harness, session string) string {
	t.Helper()
	rec := h.get("/help", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /help = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// mainOf is the page without its chrome. The header carries a display name, an
// unread-claim badge and a CSRF token, none of which is what these tests are
// comparing.
func mainOf(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "<main>")
	end := strings.Index(body, "</main>")
	if start < 0 || end < start {
		t.Fatal("no <main> in the response")
	}
	return body[start:end]
}

func idsIn(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`id="([a-z0-9-]+)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// TestHelpIsReachableWithoutSigningIn is the whole reason the route sits outside
// both middleware groups. The two things people most need explained — how to get
// an invite, and what to do about a forgotten password, neither of which
// Wishbone can email them — are on the far side of the sign-in page from anyone
// who is stuck.
func TestHelpIsReachableWithoutSigningIn(t *testing.T) {
	h := newHarness(t)

	body := helpBody(t, h, "")
	if !strings.Contains(body, `href="/login"`) {
		t.Error("a signed-out reader is not offered the way in")
	}
	if !strings.Contains(body, `id="signing-in"`) {
		t.Error("the section about signing in is missing from the page that a locked-out reader lands on")
	}

	// And the sign-in page has to offer it, or nobody stuck there will find it.
	login := h.get("/login", "").Body.String()
	if !strings.Contains(login, `href="/help#signing-in"`) {
		t.Error("the sign-in page does not link to the help page")
	}
}

// TestHelpIsOnEveryPage: the moment somebody needs help is not predictable, so
// the link is in the chrome rather than on a page you have to know to visit.
func TestHelpIsOnEveryPage(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/", "/claims", "/account", "/lists/" + h.list.ID, "/admin"} {
		body := h.get(path, h.ownerSession).Body.String()
		if !strings.Contains(body, `href="/help"`) {
			t.Errorf("%s has no Help link in the header", path)
		}
	}
}

// TestHelpLinksInTheAppAllLand is the drift guard.
//
// Several pages link straight at the part of the help page that explains the
// situation the reader is in — the item form does it four times, once per way a
// link lookup can disappoint somebody. Those fragments are only useful while
// they exist, and renaming a section is exactly the kind of edit that leaves
// them behind. So the templates are read as text, every /help fragment they
// mention is collected, and each one has to be an id on the rendered page.
func TestHelpLinksInTheAppAllLand(t *testing.T) {
	files, err := filepath.Glob("templates/*.templ")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found to scan: %v", err)
	}

	// Fetching on and an admin reader, because that is the configuration in which
	// every section of the page renders. Whether each section is present in the
	// others is TestHelpFollowsTheConfiguration's business.
	h := newHarnessOpt(t, true)
	ids := map[string]bool{}
	for _, id := range idsIn(helpBody(t, h, h.ownerSession)) {
		ids[id] = true
	}

	frag := regexp.MustCompile(`href="/help#([a-z0-9-]+)"`)
	found := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range frag.FindAllStringSubmatch(string(src), -1) {
			found++
			if !ids[m[1]] {
				t.Errorf("%s links to /help#%s, which is not a section on the help page",
					filepath.Base(f), m[1])
			}
		}
	}
	if found == 0 {
		t.Error("nothing in the app links into the help page; the scan is broken or the links were removed")
	}
}

// TestHelpHasNoDanglingLinksOfItsOwn: the contents list at the top, and the
// cross-references between sections, in every configuration the page renders in.
// Sections that depend on configuration are the easy way to end up with a
// contents entry pointing at nothing.
func TestHelpHasNoDanglingLinksOfItsOwn(t *testing.T) {
	for _, c := range []struct {
		name    string
		fetch   bool
		session func(h *harness) string
	}{
		{"lookup on, admin", true, func(h *harness) string { return h.ownerSession }},
		{"lookup on, not admin", true, func(h *harness) string { return h.claimerSession }},
		{"lookup off, admin", false, func(h *harness) string { return h.ownerSession }},
		{"lookup off, not admin", false, func(h *harness) string { return h.claimerSession }},
		{"nobody signed in", true, func(h *harness) string { return "" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarnessOpt(t, c.fetch)
			body := helpBody(t, h, c.session(h))

			ids := map[string]bool{}
			for _, id := range idsIn(body) {
				ids[id] = true
			}
			for _, m := range regexp.MustCompile(`href="#([a-z0-9-]+)"`).FindAllStringSubmatch(body, -1) {
				if !ids[m[1]] {
					t.Errorf("the page links to #%s, which is not on it", m[1])
				}
			}
		})
	}
}

// TestHelpNamesTheControlsAsTheyAreLabeled: somebody with a button in front of
// them searches the help page for the words on the button. Each label below is
// checked against the page that actually renders it, so a change to one and not
// the other fails here rather than in front of the family.
func TestHelpNamesTheControlsAsTheyAreLabeled(t *testing.T) {
	h := newHarnessOpt(t, true)
	h.addClaims()
	h.mkList(h.owner.ID, "Somewhere else") // so the owner's cards offer "Move to"

	help := helpBody(t, h, h.ownerSession)

	for _, c := range []struct {
		label   string
		path    string
		session string
	}{
		{"Add item", "/lists/" + h.list.ID, h.ownerSession},
		{"Start from a link", "/lists/" + h.list.ID + "/items/new", h.ownerSession},
		{"Look it up", "/lists/" + h.list.ID + "/items/new", h.ownerSession},
		{"Notes for whoever buys it", "/lists/" + h.list.ID + "/items/new", h.ownerSession},
		{"Move to", "/lists/" + h.list.ID, h.ownerSession},
		{"New list", "/", h.ownerSession},
		{"Everyone in the family", "/lists/" + h.list.ID, h.ownerSession},
		{"Only people I pick", "/lists/" + h.list.ID, h.ownerSession},
		{"Just me", "/lists/" + h.list.ID, h.ownerSession},
		{"Create invite link", "/admin", h.ownerSession},
		// The claim vocabulary, checked against a reader who is allowed to see it.
		{"I&rsquo;ll get this", "/lists/" + h.list.ID, h.claimerSession},
		{"still needed", "/lists/" + h.list.ID, h.claimerSession},
		{"Fully claimed", "/lists/" + h.list.ID, h.claimerSession},
		{"Mark bought", "/lists/" + h.list.ID, h.claimerSession},
		{"Release", "/lists/" + h.list.ID, h.claimerSession},
	} {
		if !strings.Contains(help, c.label) {
			t.Errorf("the help page does not mention %q, which the app shows on %s", c.label, c.path)
			continue
		}
		if !strings.Contains(h.get(c.path, c.session).Body.String(), c.label) {
			t.Errorf("%s no longer shows %q, but the help page still describes it", c.path, c.label)
		}
	}
}

// TestHelpIsTheSameForEveryReader is the property that keeps the help page out of
// the owner-blindness argument. It renders from prose and two configuration
// facts, so two people signed in as different accounts get the same page — and
// the only thing that varies at all is the extra section for an administrator,
// which is about their own tools and nobody's data.
func TestHelpIsTheSameForEveryReader(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	claimer := mainOf(t, helpBody(t, h, h.claimerSession))
	stranger := mainOf(t, helpBody(t, h, h.strangerSession))
	if claimer != stranger {
		t.Error("two readers who are not administrators got different help pages")
	}

	// The claimer has claims and the stranger has none; nothing on the page moved.
	// The badge in the header is theirs to see and is not part of this comparison.
	owner := mainOf(t, helpBody(t, h, h.ownerSession))
	if !strings.Contains(owner, `id="invites"`) {
		t.Error("an administrator is not shown the section about invites")
	}
	if strings.Contains(claimer, `id="invites"`) {
		t.Error("somebody who cannot create invites is being told how to")
	}

	// Everything else, section for section, is identical.
	strip := func(body string) []string {
		var out []string
		for _, id := range idsIn(body) {
			if id != "invites" {
				out = append(out, id)
			}
		}
		return out
	}
	if strings.Join(strip(owner), " ") != strings.Join(strip(claimer), " ") {
		t.Errorf("the administrator's help page has different sections:\n admin: %v\n other: %v",
			strip(owner), strip(claimer))
	}
}

// TestHelpDescribesThisInstance: the two facts the page is allowed to know.
//
// The share-target address is the one thing on the page nobody could work out for
// themselves — building the iPhone shortcut means typing this exact URL — so it
// is rendered rather than described.
func TestHelpDescribesThisInstance(t *testing.T) {
	t.Run("the configured address, when there is one", func(t *testing.T) {
		h := newHarness(t)
		h.cfg.BaseURL = "https://wishbone.example"

		if want := "https://wishbone.example/share-target?url="; !strings.Contains(helpBody(t, h, h.ownerSession), want) {
			t.Errorf("the shortcut instructions do not show %q", want)
		}
	})

	t.Run("otherwise the host it was asked on", func(t *testing.T) {
		h := newHarness(t)

		// httptest requests arrive as http://example.com, and this harness is not
		// on secure cookies, so that is the scheme the page should offer.
		if want := "http://example.com/share-target?url="; !strings.Contains(helpBody(t, h, h.ownerSession), want) {
			t.Errorf("the shortcut instructions do not show %q", want)
		}
	})
}

// TestHelpFollowsTheConfiguration: with link lookup switched off, every word
// about pasting a link describes a box that is not on the screen. The published
// manifests default it off, so this is a real configuration and not a
// hypothetical one.
func TestHelpFollowsTheConfiguration(t *testing.T) {
	on := helpBody(t, newHarnessOpt(t, true), "")
	if !strings.Contains(on, `id="link-lookup"`) {
		t.Error("with lookup on, the page does not explain it")
	}

	off := helpBody(t, newHarnessOpt(t, false), "")
	if strings.Contains(off, `id="link-lookup"`) {
		t.Error("with lookup off, the page still explains a feature this instance does not have")
	}
	if !strings.Contains(off, "every item is typed in") {
		t.Error("with lookup off, the page does not say so")
	}
}
