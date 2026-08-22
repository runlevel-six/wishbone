package extract

import (
	"net/url"
	"strings"
	"unicode"
)

// MaxSlugTitle is the item title limit the form enforces.
const MaxSlugTitle = 200

// TitleFromURL recovers a likely product name from the address itself.
//
// This exists for the pages nothing can read. When a shop refuses the request
// there is no title, no price and no picture, and the person is left typing the
// name of the thing by hand — on a phone, exactly, including the model number.
// But shops that put the product in the path have already written the name
// down, and the person pasted it:
//
//	/p/SOMEBRAND-Cordless-1-2-in-Ratchet-Tool-Only-ABC123B/318631225
//	  -> SOMEBRAND Cordless 1 2 in Ratchet Tool Only ABC123B
//
// This is not extraction and makes no claim about the product. It restates the
// address the person is looking at, which is why it is safe where a parse of
// page content would not be: it is offered filled in and labeled as a guess,
// and the price — the field that turns a mistake into the wrong present — is
// never touched.
//
// Returns "" when the address carries nothing name-like, which includes every
// address whose product segment is a bare identifier. Silence is the default;
// a bad guess is worse than none, because it is the title somebody saves
// without reading.
func TitleFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}

	// The longest name-like segment, rather than a per-host rule about which
	// position holds it. Shops disagree about the position — /p/<slug>/<id>,
	// /pd/<slug>/<id>, /ip/<slug>/<id>, /products/<slug> — and agree that the
	// slug is the longest thing in the path, because it is prose and the rest
	// are identifiers.
	best := ""
	for _, seg := range rawPathSegments(u.Path) {
		if cand := nameLikeSegment(seg); len(cand) > len(best) {
			best = cand
		}
	}
	if best == "" {
		return ""
	}

	title := strings.Join(strings.Fields(strings.ReplaceAll(best, "-", " ")), " ")

	// Deliberately no attempt to turn "1-2-in" back into "1/2 in". The same
	// shape means different things — "1-2-in" is a fraction and "2-0-Ah" is a
	// decimal — and no rule tells them apart from the slug alone. Both appear in
	// real addresses. A reader fixes "1 2 in" in a second because it is visibly
	// unfinished; a confident "2/0 Ah" is the kind of quietly wrong detail this
	// project refuses to produce elsewhere, and it would be no better here.
	if len(title) > MaxSlugTitle {
		title = title[:MaxSlugTitle]
		if i := strings.LastIndexByte(title, ' '); i > 0 {
			title = title[:i]
		}
	}
	return title
}

// nameLikeSegment returns the segment if it reads like a product name, or "".
func nameLikeSegment(seg string) string {
	seg = strings.TrimSuffix(seg, ".html")
	seg = strings.TrimSuffix(seg, ".htm")
	// Generous: a real slug is nowhere near this, and over-long input is
	// truncated to the form's limit by the caller rather than discarded.
	if seg == "" || len(seg) > 512 {
		return ""
	}

	// Prose is hyphenated. One word is not enough to be worth offering and is
	// where the false positives live: a category name, a path component like
	// "products", a bare model number.
	words := strings.Split(seg, "-")
	if len(words) < 2 {
		return ""
	}

	for _, r := range seg {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == '-' || r == '_' || r == '.' || r == '+' || r == '\'':
		default:
			// Percent-encoding, punctuation, anything else: this is not a name
			// somebody wrote, it is machinery or a search query.
			return ""
		}
	}

	// Two words that read as words, where a word means a run of at least three
	// letters together.
	//
	// The run is what does the work, and a plain letter count will not do it.
	// Hex is mostly letters: a UUID, or an id like "a1b2c3d4-e5f6", passes any
	// ratio test and reads as gibberish. What those never have is three letters
	// in a row, because their letters are separated by digits — while "Cordless",
	// "Brushless" and "Ratchet" are nothing but runs. Requiring two of them also
	// drops a lone identifier carrying a hyphen, like "N-5yc1vZm5d".
	wordy := 0
	for _, w := range words {
		if longestLetterRun(w) >= 3 {
			wordy++
		}
	}
	if wordy < 2 {
		return ""
	}
	return seg
}

// longestLetterRun returns the length of the longest run of consecutive letters.
func longestLetterRun(s string) int {
	best, run := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			run++
			if run > best {
				best = run
			}
			continue
		}
		run = 0
	}
	return best
}
