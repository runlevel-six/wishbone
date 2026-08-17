package templates

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// csrfHeader renders the hx-headers attribute configured once on <body>, so
// every htmx request carries the CSRF token (plan §4).
func csrfHeader(token string) string {
	b, err := json.Marshal(map[string]string{"X-CSRF-Token": token})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// hxVals renders an hx-vals attribute from alternating key/value pairs, so a
// button can post a value the surrounding form does not carry.
func hxVals(kv ...string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// believedLookup reports whether the form is showing a lookup that ran, was
// believed, and was applied on its own. Every other outcome — held back by the
// guard, accepted under protest, refused by the retailer — has its own flash
// saying so, and must not also be described as an ordinary success.
func believedLookup(f ItemFormData) bool {
	return f.Extracted && !f.Suspect && !f.Blocked && !f.Accepted
}

// priceLabel joins a price with its currency when there is one.
func priceLabel(price, currency string) string {
	if currency == "" {
		return price
	}
	return price + " " + currency
}

// ellipsize shortens text for display only. It cuts on a rune boundary so a
// truncated multi-byte character cannot land in the output.
func ellipsize(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimRight(string(r[:max]), " ") + "…"
}

func navClass(path, target string) string {
	if path == target || (target != "/" && strings.HasPrefix(path, target)) {
		return "active"
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func itoa(n int) string { return strconv.Itoa(n) }

// friendlyDate renders a stored RFC3339 timestamp as a short date.
func friendlyDate(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return t.Local().Format("Jan 2, 2006")
}

// asset appends the build version to a static URL.
//
// Three separate caches hold these files and none of them can be told to let go:
// the HTTP cache honours an hour of max-age, browsers keep a favicon for far
// longer than they are told to, and the service worker matches by URL. A new URL
// is the only instruction all three obey. An empty version — tests, and `go run`
// during development — leaves the URL alone, so nothing has to special-case it.
func asset(path, version string) string {
	if version == "" {
		return path
	}
	return path + "?v=" + url.QueryEscape(version)
}

// claimUpdatesTitle explains the badge without naming an item. The nav is on
// every page including a list owner's own, and a tooltip is the wrong place to
// start describing which of somebody's claims changed.
func claimUpdatesTitle(n int) string {
	return plural(n, "item you claimed has changed", "items you claimed have changed")
}

// addedLabel says when an item was added, relatively while that is the useful
// framing and absolutely once it is not (plan §14).
//
// The point is a purchase decision, not a changelog. "Added 3 days ago" says
// the owner still wants this; "Added March 12, 2024" says ask them before you
// spend the money. A bare date does the first job badly and a bare age does the
// second job badly, so the wording switches at the month mark, which is roughly
// where an age stops being something people can picture.
func addedLabel(ts string) string {
	return addedLabelAt(ts, time.Now())
}

func addedLabelAt(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		// A clock skew or a bad row. Say the date rather than "in -3 days".
		return "Added " + t.Local().Format("January 2, 2006")
	case d < 24*time.Hour:
		return "Added today"
	case d < 48*time.Hour:
		return "Added yesterday"
	case d < 7*24*time.Hour:
		return "Added " + plural(int(d/(24*time.Hour)), "day ago", "days ago")
	case d < 30*24*time.Hour:
		return "Added " + plural(int(d/(7*24*time.Hour)), "week ago", "weeks ago")
	default:
		return "Added " + t.Local().Format("January 2, 2006")
	}
}

// exactTimestamp is the full moment, for the title attribute behind a relative
// label. Anyone who wants the precise answer can hover for it.
func exactTimestamp(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return t.Local().Format("January 2, 2006 at 3:04 PM")
}

// linkStatusNote is the owner-facing explanation of a link check. It is never
// shown to claimers: a claimer seeing link warnings would leak nothing, but the
// job that produces them runs per owner, and keeping the surface small keeps
// plan §5.4's "owner only" rule easy to verify.
func linkStatusNote(status string) string {
	switch status {
	case "suspect":
		return "This link may no longer point at the right product."
	case "dead":
		return "This link did not load last time it was checked."
	default:
		return ""
	}
}

func visibilityLabel(v string) string {
	switch v {
	case "all_users":
		return "Everyone in the family"
	case "selected":
		return "Only people you pick"
	case "private":
		return "Just you"
	default:
		return v
	}
}

// availabilityLabel describes what is left of an item. It is only ever
// rendered for non-owners: the owner's card type carries no claim data.
func availabilityLabel(available, quantity int) string {
	if quantity <= 1 {
		return "Available"
	}
	return fmt.Sprintf("%d of %d still needed", available, quantity)
}

func queryEscape(s string) string { return url.QueryEscape(s) }

// lookupTrigger fires the lookup on page load when a link arrived from a share
// sheet, and otherwise leaves the form on its normal submit trigger.
func lookupTrigger(f ItemFormData) string {
	if f.AutoLookup {
		return "load, submit"
	}
	return "submit"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// formAction points the item form at create or update.
func formAction(f ItemFormData) string {
	if f.ItemID == "" {
		return "/lists/" + f.ListID + "/items"
	}
	return "/items/" + f.ItemID
}

// currentCategory finds the selected category so its fields render on first
// paint without a round trip.
func currentCategory(f ItemFormData) CategoryOption {
	for _, c := range f.Categories {
		if c.ID == f.CategoryID {
			return c
		}
	}
	if len(f.Categories) > 0 {
		return f.Categories[0]
	}
	return CategoryOption{}
}

// sourcesJSON round-trips items.field_sources through the form, so a save does
// not lose the record of which extractor supplied which field.
func sourcesJSON(sources map[string]string) string {
	if len(sources) == 0 {
		return "{}"
	}
	b, err := json.Marshal(sources)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// otherErrors lists validation problems that have no inline home on the form —
// notably the per-attribute ones, whose inputs are rendered from a category
// schema rather than written out field by field.
func otherErrors(errs map[string]string) []string {
	inline := map[string]bool{"title": true, "price": true, "quantity": true}
	keys := make([]string, 0, len(errs))
	for k := range errs {
		if !inline[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		label := strings.TrimPrefix(k, "attr_")
		out = append(out, strings.ToUpper(label[:1])+label[1:]+": "+errs[k])
	}
	return out
}
