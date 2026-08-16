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
