package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"wishd/internal/web/templates"
)

// handleShareTarget receives a link shared from a phone.
//
// On Android this is wired up by the web manifest's share_target, so Wishbone
// appears in the system share sheet. iOS Safari has no equivalent, so iPhone
// users reach the same endpoint through a Shortcut that opens
// /share-target?url=… — one endpoint, both platforms, documented in
// docs/how-to/add-from-your-phone.md.
//
// Whatever arrives, the goal is the same: get to an add-item form with the
// lookup already running, in as few taps as possible.
func (s *Server) handleShareTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	shared := sharedURL(r.URL.Query())
	if shared == "" {
		s.flash(w, templates.FlashWarn, "That share did not contain a link. You can still add the item by hand.")
		s.redirect(w, r, "/")
		return
	}

	lists, err := s.st.ListsOwnedBy(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	switch len(lists) {
	case 0:
		// Nowhere to put it yet. Say so rather than dropping the link.
		s.flash(w, templates.FlashInfo, "Make a list first, then share the link again.")
		s.redirect(w, r, "/")
	case 1:
		// The common case for most people: straight to the form.
		s.redirect(w, r, "/lists/"+lists[0].ID+"/items/new?url="+url.QueryEscape(shared))
	default:
		var opts []templates.ShareListOption
		for _, l := range lists {
			opts = append(opts, templates.ShareListOption{ID: l.ID, Name: l.Name})
		}
		s.render(w, r, http.StatusOK,
			templates.SharePicker(s.page(w, r, "Add to which list?"), shared, opts))
	}
}

// urlInText finds a link inside shared text. Android apps frequently share
// "Some product name https://shop.example/thing" as one blob of text rather
// than filling in the url field.
var urlInText = regexp.MustCompile(`https?://[^\s<>"']+`)

func sharedURL(q url.Values) string {
	if v := strings.TrimSpace(q.Get("url")); v != "" {
		return v
	}
	for _, field := range []string{"text", "title"} {
		if m := urlInText.FindString(q.Get(field)); m != "" {
			return strings.TrimRight(m, ".,)")
		}
	}
	return ""
}

// safeNext sanitizes a post-login redirect target.
//
// Only same-site paths are allowed. An open redirect here would be a genuine
// phishing primitive: a link to the real Wishbone host that bounces to an
// attacker's copy of the sign-in page after authenticating.
func safeNext(next string) string {
	if next == "" {
		return ""
	}
	// "//evil.example" and "/\evil.example" are both browser-relative to
	// another origin, and a backslash is treated as a slash by some parsers.
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return ""
	}
	return u.RequestURI()
}
