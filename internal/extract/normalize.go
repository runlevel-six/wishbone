package extract

import (
	"net/url"
	"regexp"
	"strings"
)

// Tracking parameters stripped during normalization (plan §5.1).
var trackingParams = map[string]bool{
	"ref": true, "ref_": true, "tag": true, "mrntrk": true, "psc": true,
	"th": true, "s": true, "l": true, "gclid": true, "fbclid": true,
	"mc_cid": true, "mc_eid": true, "_branch_match_id": true, "smid": true,
	"linkCode": true, "linkId": true, "creativeASIN": true, "ascsubtag": true,
	"pd_rd_i": true, "pd_rd_r": true, "pd_rd_w": true, "pd_rd_wg": true,
	"pf_rd_p": true, "pf_rd_r": true, "content-id": true, "qid": true,
	"sprefix": true, "sr": true, "crid": true, "dib": true, "dib_tag": true,
	"keywords": true, "spm": true, "srsltid": true,
}

var asinRe = regexp.MustCompile(`(?i)/(?:dp|gp/product|gp/aw/d|product)/([A-Z0-9]{10})(?:[/?]|$)`)

// NormalizeURL cleans a user-pasted URL for storage and duplicate detection.
//
// The scheme is preserved rather than forced to https here: the plan wants
// https "where the host redirects to it", and the honest way to learn that is
// to re-normalize the fetcher's final URL after the redirect chain, which
// AfterFetch does.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errUnsupportedScheme
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.RawFragment = ""

	// AmazonSmile was discontinued; the legacy data is full of these.
	host := strings.TrimPrefix(u.Host, "www.")
	if host == "smile.amazon.com" || strings.HasPrefix(host, "smile.amazon.") {
		u.Host = "www." + strings.TrimPrefix(host, "smile.")
	}

	if isAmazonHost(u.Host) {
		if m := asinRe.FindStringSubmatch(u.Path); m != nil {
			// Canonical Amazon product form; everything after the ASIN is
			// navigation state, not identity.
			u.Path = "/dp/" + strings.ToUpper(m[1])
			u.RawQuery = ""
		}
	}

	q := u.Query()
	for k := range q {
		if trackingParams[k] || strings.HasPrefix(strings.ToLower(k), "utm_") {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()

	if u.Path == "/" {
		u.Path = ""
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if u.Path == "" && u.RawQuery == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

// AfterFetch re-normalizes the URL the fetcher actually landed on. This is
// where an http URL becomes https, because the host proved it redirects.
func AfterFetch(requested string, final *url.URL) string {
	if final == nil {
		return requested
	}
	n, err := NormalizeURL(final.String())
	if err != nil || n == "" {
		return requested
	}
	return n
}

func isAmazonHost(host string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	return host == "amazon.com" || strings.HasPrefix(host, "amazon.") ||
		strings.HasSuffix(host, ".amazon.com")
}
