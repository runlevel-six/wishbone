package extract

import "strings"

// Following a redirect that was never an HTTP redirect.
//
// A server component can redirect by throwing, and on a streamed response the
// framework has already sent the 200 and the opening shell by the time that
// happens. It cannot take the status code back, so the redirect goes out inside
// the payload instead, as an error digest:
//
//	20:E{"digest":"NEXT_REDIRECT;replace;https://shop.example.com/p/real-slug/ID.html;307;"}
//
// React's runtime acts on it in the browser. Nothing that does not execute
// JavaScript ever leaves the first address.
//
// The worked example is a department store that keeps aliases for its product
// paths — an older slug, or one spelled with the punctuation of the product
// name rather than the brand prefix it settled on. Asking for an alias returns
// a complete 1 MB `200`: the navigation shell, the mega-menu, 66 payload
// chunks, an unresolved Suspense boundary where the product should be, and the
// digest above. No title, no OpenGraph, no JSON-LD, and the string "price" not
// present once in the whole megabyte. Every tier correctly reports nothing,
// the guard has nothing to doubt, and the form comes back blank with no
// explanation — the pasted link is fine, the shop is happy, and the person is
// left retyping what the page would have said at the other address.
//
// So a payload is asked whether it redirected, and if it did, that address is
// fetched instead. Which is what the browser does; the only difference is that
// the browser was told in JavaScript.
const nextRedirectMarker = "NEXT_REDIRECT"

// flightRedirect returns the single address an RSC payload redirected to, or ""
// when it did not redirect or named more than one place.
//
// Ambiguity is refused for the same reason recoverFlightJSONLD refuses it: one
// payload can carry the errors of several nested segments, and following the
// wrong one lands on a page that is not the product. One target is a page
// saying where it lives. Two are a guess.
func flightRedirect(chunks []string) string {
	if len(chunks) == 0 {
		return ""
	}
	stream := flightStream(chunks)

	var targets []string
	for i := 0; ; {
		m := strings.Index(stream[i:], nextRedirectMarker)
		if m < 0 {
			break
		}
		i += m + len(nextRedirectMarker)
		if t := redirectTargetIn(digestAt(stream, i)); t != "" {
			targets = append(targets, t)
		}
	}

	if targets = dedupeStrings(targets); len(targets) == 1 {
		return targets[0]
	}
	return ""
}

// digestAt returns the rest of the digest string beginning at i, which is just
// past the marker. The digest is a JSON string value in the payload, so it ends
// at the next quote; the newline bound is belt and braces for a payload that is
// not shaped the way this expects.
func digestAt(stream string, i int) string {
	rest := stream[i:]
	if end := strings.IndexAny(rest, "\"\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// redirectTargetIn picks the address out of a digest's semicolon-separated
// fields. The other fields are the marker's own trailing text, the kind of
// navigation ("replace", "push") and the status code, and their order has
// changed between framework releases — so the address is found by being one
// rather than by its position.
//
// A protocol-relative "//host/path" is not accepted: it reads as a path and
// resolves to another host, and the caller's same-site check is the wrong place
// to notice that.
func redirectTargetIn(digest string) string {
	for _, field := range strings.Split(digest, ";") {
		field = strings.TrimSpace(field)
		switch {
		case strings.HasPrefix(field, "https://"), strings.HasPrefix(field, "http://"):
			return field
		case strings.HasPrefix(field, "/") && !strings.HasPrefix(field, "//"):
			return field
		}
	}
	return ""
}
