package web

import (
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"

	"wishbone/internal/model"
)

func themeAttr(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`<html lang="en" data-theme="([a-z]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no data-theme on <html>; the stylesheet has nothing to switch on")
	}
	return m[1]
}

func metaThemeColor(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`<meta name="theme-color" content="(#[0-9a-fA-F]{6})"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no theme-color meta tag")
	}
	return strings.ToLower(m[1])
}

// TestDefaultThemeIsTheBrandGreen covers the two places a palette is not chosen:
// a fresh account, and nobody at all.
func TestDefaultThemeIsTheBrandGreen(t *testing.T) {
	h := newHarness(t)

	if got := themeAttr(t, h.get("/", h.ownerSession).Body.String()); got != string(model.ThemeForest) {
		t.Errorf("a fresh account renders %q, want the default", got)
	}
	if got := themeAttr(t, h.get("/login", "").Body.String()); got != string(model.ThemeForest) {
		t.Errorf("the sign-in page renders %q; nobody is signed in, so it has no preference to read", got)
	}
	if u, err := h.st.UserByID(t.Context(), h.owner.ID); err != nil {
		t.Fatal(err)
	} else if u.Theme != model.ThemeForest {
		t.Errorf("stored theme for a new account is %q, want the default", u.Theme)
	}
}

// TestChoosingATheme is the whole feature from the outside: pick one, and every
// page comes back in it.
func TestChoosingATheme(t *testing.T) {
	h := newHarness(t)

	rec := h.post("/account/theme", h.ownerSession, url.Values{"theme": {"cranberry"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("saving a theme: status %d, want 303", rec.Code)
	}

	for _, path := range []string{"/", "/account", "/lists/" + h.list.ID, "/claims"} {
		body := h.get(path, h.ownerSession).Body.String()
		if got := themeAttr(t, body); got != "cranberry" {
			t.Errorf("%s renders %q after the choice was saved", path, got)
		}
		// The phone paints its own browser chrome from this, so it has to move
		// with the rest of the page.
		if got := metaThemeColor(t, body); got != "#b83a3a" {
			t.Errorf("%s carries theme-color %s, want the cranberry accent", path, got)
		}
	}

	// The picker shows every palette, with the saved one selected.
	account := h.get("/account", h.ownerSession).Body.String()
	for _, theme := range model.Themes {
		if !strings.Contains(account, `value="`+string(theme)+`"`) {
			t.Errorf("the picker does not offer %q", theme)
		}
		if !strings.Contains(account, "swatch-"+string(theme)) {
			t.Errorf("%q has no swatch, so the choice is a word rather than a color", theme)
		}
	}
	if !strings.Contains(account, `value="cranberry" checked`) {
		t.Error("the picker does not show which palette is switched on")
	}
}

// TestThemeIsOnePersonsChoice: a preference on the account, not a setting on the
// instance.
func TestThemeIsOnePersonsChoice(t *testing.T) {
	h := newHarness(t)

	if rec := h.post("/account/theme", h.ownerSession, url.Values{"theme": {"navy"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("saving: status %d", rec.Code)
	}
	if got := themeAttr(t, h.get("/", h.ownerSession).Body.String()); got != "navy" {
		t.Errorf("the person who chose navy sees %q", got)
	}
	if got := themeAttr(t, h.get("/", h.claimerSession).Body.String()); got != string(model.ThemeForest) {
		t.Errorf("somebody else's page changed to %q", got)
	}
	// Including the list they share: a theme is chrome, not content.
	if got := themeAttr(t, h.get("/lists/"+h.list.ID, h.claimerSession).Body.String()); got != string(model.ThemeForest) {
		t.Errorf("the owner's choice followed the list to another reader: %q", got)
	}
}

// TestUnknownThemeFallsBack: a hand-made request cannot leave an account
// pointing at a palette the stylesheet has never heard of, which would render as
// a page with no colors at all.
func TestUnknownThemeFallsBack(t *testing.T) {
	h := newHarness(t)

	for _, junk := range []string{"tangerine", "", "; rm -rf /", "FOREST"} {
		if rec := h.post("/account/theme", h.ownerSession, url.Values{"theme": {junk}}); rec.Code != http.StatusSeeOther {
			t.Errorf("theme=%q: status %d, want 303", junk, rec.Code)
		}
		if got := themeAttr(t, h.get("/", h.ownerSession).Body.String()); got != string(model.ThemeForest) {
			t.Errorf("theme=%q left the page on %q", junk, got)
		}
		u, err := h.st.UserByID(t.Context(), h.owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if u.Theme != model.ThemeForest {
			t.Errorf("theme=%q was stored as %q", junk, u.Theme)
		}
	}
}

// cssBlock is one rule set from the stylesheet: which palette it is for, whether
// it applies in dark mode, and the variables it sets.
type cssBlock struct {
	selector string
	dark     bool
	vars     map[string]string
}

// parseThemeCSS pulls the palette rule sets out of the served stylesheet.
//
// It reads what the server actually serves rather than the file on disk, so a
// stylesheet that never made it into the binary's embedded copy fails here too.
func parseThemeCSS(t *testing.T, h *harness) []cssBlock {
	t.Helper()
	css := h.get("/static/app.css", "").Body.String()
	if len(css) < 1000 {
		t.Fatalf("the stylesheet came back as %d bytes", len(css))
	}
	// Comments first: they contain selector-like prose, and one of them names
	// [data-theme=…] while explaining the rules below.
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")

	themeSel := regexp.MustCompile(`^\[data-theme="([a-z]+)"\]$`)
	varDecl := regexp.MustCompile(`^(--[a-z-]+)\s*:\s*([^;]+);$`)

	var out []cssBlock
	var cur *cssBlock
	depth, dark := 0, false

	for _, raw := range strings.Split(css, "\n") {
		line := strings.TrimSpace(raw)
		opens, closes := strings.Count(line, "{"), strings.Count(line, "}")
		switch {
		case opens > 0 && closes == 0:
			sel := strings.TrimSpace(strings.TrimSuffix(line, "{"))
			if depth == 0 && strings.HasPrefix(sel, "@media") && strings.Contains(sel, "prefers-color-scheme: dark") {
				dark = true
			}
			if sel == ":root" || themeSel.MatchString(sel) {
				out = append(out, cssBlock{selector: sel, dark: dark, vars: map[string]string{}})
				cur = &out[len(out)-1]
			}
			depth += opens
		case closes > 0 && opens == 0:
			depth -= closes
			cur = nil
			if depth == 0 {
				dark = false
			}
		case cur != nil:
			if m := varDecl.FindStringSubmatch(line); m != nil {
				cur.vars[m[1]] = strings.TrimSpace(m[2])
			}
		}
	}
	return out
}

func keysOf(vars map[string]string) []string {
	out := make([]string, 0, len(vars))
	for k := range vars {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func findBlock(blocks []cssBlock, selector string, dark bool) *cssBlock {
	for i := range blocks {
		if blocks[i].selector == selector && blocks[i].dark == dark {
			return &blocks[i]
		}
	}
	return nil
}

// TestEveryThemeIsComplete is the guard that makes adding a palette safe.
//
// Two ways a theme can be half-finished, both of which look fine on the machine
// of whoever added it and wrong on somebody else's: leaving a variable out that
// the other palettes set, and setting one in light mode without setting it in
// dark. The second is the sneaky one — [data-theme=…] and :root have identical
// specificity, so in dark mode a theme's light block still applies to anything
// its dark block does not mention, and the leftover is a light-mode color on a
// near-black ground.
func TestEveryThemeIsComplete(t *testing.T) {
	h := newHarness(t)
	blocks := parseThemeCSS(t, h)

	root := findBlock(blocks, ":root", false)
	if root == nil {
		t.Fatal("no :root block in the stylesheet")
	}

	// The default palette is :root itself and has no block of its own; every
	// other palette needs one in each mode.
	var light, dark []*cssBlock
	for _, theme := range model.Themes {
		if theme == model.ThemeForest {
			if findBlock(blocks, `[data-theme="`+string(theme)+`"]`, false) != nil {
				t.Errorf("%q has a block of its own as well as being the :root default", theme)
			}
			continue
		}
		sel := `[data-theme="` + string(theme) + `"]`
		lb, db := findBlock(blocks, sel, false), findBlock(blocks, sel, true)
		if lb == nil {
			t.Errorf("%s has no light-mode block", sel)
			continue
		}
		if db == nil {
			t.Errorf("%s has no dark-mode block", sel)
			continue
		}
		light, dark = append(light, lb), append(dark, db)

		// Rule 2: what it sets in light it sets again in dark.
		for _, k := range keysOf(lb.vars) {
			if _, ok := db.vars[k]; !ok {
				t.Errorf("%s sets %s in light mode but not in dark, so the light value survives on a dark ground", sel, k)
			}
		}
		// A variable no other palette has is a typo, not a feature.
		for _, k := range keysOf(db.vars) {
			if _, ok := root.vars[k]; !ok {
				t.Errorf("%s sets %s, which :root does not define anywhere", sel, k)
			}
		}
	}

	// Rule 1: every palette covers the same ground as the others.
	for _, mode := range []struct {
		name   string
		blocks []*cssBlock
	}{{"light", light}, {"dark", dark}} {
		for i := 1; i < len(mode.blocks); i++ {
			first, other := mode.blocks[0], mode.blocks[i]
			if got, want := strings.Join(keysOf(other.vars), " "), strings.Join(keysOf(first.vars), " "); got != want {
				t.Errorf("%s mode: %s sets\n  %s\nbut %s sets\n  %s",
					mode.name, other.selector, got, first.selector, want)
			}
		}
	}
}

// TestSwatchesAndMetaColorAgree: the palette's hex appears in three places — the
// stylesheet's constant, the swatch in the picker (which uses that constant), and
// the theme-color meta tag, which cannot read a CSS variable and so repeats the
// value in Go. This is the seam where they could drift.
func TestSwatchesAndMetaColorAgree(t *testing.T) {
	h := newHarness(t)
	root := findBlock(parseThemeCSS(t, h), ":root", false)
	if root == nil {
		t.Fatal("no :root block")
	}

	for _, theme := range model.Themes {
		swatch, ok := root.vars["--swatch-"+string(theme)]
		if !ok {
			t.Errorf("no --swatch-%s in the stylesheet; the picker has nothing to paint with", theme)
			continue
		}
		if rec := h.post("/account/theme", h.ownerSession, url.Values{"theme": {string(theme)}}); rec.Code != http.StatusSeeOther {
			t.Fatalf("saving %q: status %d", theme, rec.Code)
		}
		got := metaThemeColor(t, h.get("/", h.ownerSession).Body.String())
		if got != strings.ToLower(swatch) {
			t.Errorf("%q: theme-color is %s, --swatch-%s is %s", theme, got, theme, swatch)
		}
	}
}

// TestMarkMatchesTheStaticIcon: the bow is now drawn in three places — the
// inline component, static/icon.svg for the favicon, and tools/icongen for the
// PNGs. The first two are compared here so the copies cannot drift into two
// different bows.
func TestMarkMatchesTheStaticIcon(t *testing.T) {
	h := newHarness(t)

	paths := func(svg string) []string {
		var out []string
		for _, m := range regexp.MustCompile(`<path d="([^"]+)"`).FindAllStringSubmatch(svg, -1) {
			out = append(out, strings.Join(strings.Fields(m[1]), " "))
		}
		return out
	}

	static := paths(h.get("/static/icon.svg", "").Body.String())
	if len(static) == 0 {
		t.Fatal("no paths in static/icon.svg")
	}
	inline := paths(h.get("/", h.ownerSession).Body.String())
	if strings.Join(inline, "|") != strings.Join(static, "|") {
		t.Errorf("the inline mark and the favicon are different drawings:\n inline: %v\n static: %v", inline, static)
	}
}
