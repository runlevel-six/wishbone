package extract

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
)

// Recovering JSON-LD from a React Server Components payload.
//
// A Next.js App Router page does not serve its JSON-LD as a script tag. It
// serves a description of one. The server component tree arrives as a stream of
//
//	self.__next_f.push([1,"<chunk>"])
//
// calls whose chunks concatenate into a single payload, and inside that payload
// the JSON-LD sits as a prop:
//
//	["$","script",null,{"type":"application/ld+json",
//	  "dangerouslySetInnerHTML":{"__html":"{\"@context\":\"https://schema.org\"…"}}]
//
// The browser turns that into a real <script> when it hydrates. Nothing that
// does not run JavaScript ever sees one, which is why the JSONLD tier finds
// zero blocks on a page whose product data is complete and sitting in the bytes
// we already paid to fetch. A department store chain's dress-shirt page is the
// worked example: name, sku, brand, a sale price and a list price, none of it
// reachable through a script tag.
//
// Two details make this less trivial than a regex over the response:
//
//   - The payload is chunked at arbitrary offsets, so a single JSON-LD blob can
//     straddle two pushes. Everything must be concatenated before anything is
//     extracted. Scanning chunk by chunk silently loses whichever blobs happen
//     to land on a boundary, which is the kind of bug that looks like "that
//     retailer just doesn't publish structured data".
//   - There are two levels of escaping: the chunk is a JSON string, and the
//     __html value inside it is a JSON string again. Both are undone with the
//     JSON decoder rather than by unescaping by hand, because hand-rolled
//     unescaping gets \\" and \u sequences wrong in ways that corrupt prices.
//
// What is deliberately conservative: a payload can describe more than one
// product — recommendation carousels put their neighbours in the same stream —
// and picking the wrong one yields a confidently wrong item, which is the exact
// failure the soft-404 guard exists to prevent. So when the payload is
// ambiguous, this refuses rather than guesses. See recoverFlightJSONLD.

const flightPushMarker = "__next_f.push("

// isFlightChunk reports whether an inline script is part of an RSC payload.
// Called on every inline script in the document, so it stays a substring test.
func isFlightChunk(script []byte) bool {
	return bytes.Contains(script, []byte(flightPushMarker))
}

// flightStream concatenates the payload back out of its push calls.
//
// Chunks are joined in document order, which is the order the server wrote
// them; that is what makes a straddling blob whole again.
func flightStream(chunks []string) string {
	var b strings.Builder
	for _, chunk := range chunks {
		for i := 0; ; {
			m := strings.Index(chunk[i:], flightPushMarker)
			if m < 0 {
				break
			}
			i += m + len(flightPushMarker)
			s, next, ok := flightPushString(chunk, i)
			if !ok {
				continue
			}
			b.WriteString(s)
			i = next
		}
	}
	return b.String()
}

// flightPushString reads the string argument of a push call that starts at i,
// which is just past "__next_f.push(". The shape is [<number>,"<payload>"];
// forms without a string argument — push([0]) marking the end of the stream —
// are not an error, they simply have nothing to contribute.
func flightPushString(s string, i int) (string, int, bool) {
	i = skipSpace(s, i)
	if i >= len(s) || s[i] != '[' {
		return "", i, false
	}
	i = skipSpace(s, i+1)
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	i = skipSpace(s, i)
	if i >= len(s) || s[i] != ',' {
		return "", i, false
	}
	i = skipSpace(s, i+1)
	if i >= len(s) || s[i] != '"' {
		return "", i, false
	}
	return jsonStringAt(s, i)
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// jsonStringAt decodes the JSON string literal beginning at s[i], which must be
// its opening quote, and returns the value and the index just past its closing
// quote.
//
// The scan has to respect backslash escapes to find the real end: a payload
// full of \" would otherwise terminate at the first one. Decoding is left to
// encoding/json once the bounds are known.
func jsonStringAt(s string, i int) (string, int, bool) {
	if i >= len(s) || s[i] != '"' {
		return "", i, false
	}
	for j := i + 1; j < len(s); {
		switch s[j] {
		case '\\':
			j += 2
		case '"':
			var out string
			if err := json.Unmarshal([]byte(s[i:j+1]), &out); err != nil {
				return "", j + 1, false
			}
			return out, j + 1, true
		default:
			j++
		}
	}
	return "", len(s), false
}

// recoverFlightJSONLD returns the JSON-LD documents carried in an RSC payload,
// ready to be appended to Page.JSONLD.
//
// want is the address the page was actually fetched from; it decides which
// product is this page's when the payload describes more than one.
//
// The rule when there is ambiguity is to refuse. A payload with several Product
// nodes is a page with a carousel, and the extractor has no way to tell the
// item from its neighbours except by the address each one claims. So:
//
//   - one Product-bearing document: take it. A page that describes exactly one
//     product is describing itself.
//   - several: take only those whose @id or url is this page's address, and if
//     that matches none of them, take nothing. Half the data is recoverable
//     from OpenGraph and the manual path is always there; a wrong price is not
//     recoverable at all, because nobody checks a field that looks filled in.
func recoverFlightJSONLD(chunks []string, want *url.URL) []string {
	if len(chunks) == 0 {
		return nil
	}
	stream := flightStream(chunks)
	if stream == "" {
		return nil
	}

	var docs []string
	var matching []string
	const key = `"__html"`

	for i := 0; ; {
		m := strings.Index(stream[i:], key)
		if m < 0 {
			break
		}
		i = skipSpace(stream, i+m+len(key))
		if i >= len(stream) || stream[i] != ':' {
			continue
		}
		i = skipSpace(stream, i+1)
		blob, next, ok := jsonStringAt(stream, i)
		if !ok {
			// Nothing decodable here; step past the key rather than the value
			// so a malformed blob cannot stall the scan.
			i += len(key)
			continue
		}
		i = next

		// Most __html values are inline CSS or a bot-detection snippet. Only
		// the ones that parse as JSON describing a Product are of interest.
		var doc any
		if err := json.Unmarshal([]byte(strings.TrimSpace(blob)), &doc); err != nil {
			continue
		}
		nodes := flattenLD(doc)
		claimed := ""
		found := false
		for _, node := range nodes {
			if !isType(node, "Product") {
				continue
			}
			found = true
			if claimed == "" {
				claimed = nodeURL(node)
			}
		}
		if !found {
			continue
		}
		docs = append(docs, blob)
		if want != nil && claimed != "" && sameProductAddress(claimed, want) {
			matching = append(matching, blob)
		}
	}

	switch {
	case len(docs) == 1:
		return docs
	case len(matching) > 0:
		return matching
	default:
		return nil
	}
}

// nodeURL returns the address a Product node claims for itself. @id is the
// usual carrier on a streamed payload; url is the schema.org-blessed one.
func nodeURL(node map[string]any) string {
	for _, k := range []string{"@id", "url"} {
		if s, ok := node[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// sameProductAddress reports whether a claimed address is the page's own.
//
// Host and path only: the claim is routinely written without the query string
// or fragment the visitor arrived with, and a trailing slash is not a different
// product.
func sameProductAddress(claimed string, want *url.URL) bool {
	cu, err := want.Parse(strings.TrimSpace(claimed))
	if err != nil {
		return false
	}
	if !strings.EqualFold(cu.Host, want.Host) {
		return false
	}
	return strings.TrimSuffix(cu.Path, "/") == strings.TrimSuffix(want.Path, "/")
}
