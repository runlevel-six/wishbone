# Extraction

What happens between pasting a URL and a filled-in form.

## Pipeline

1. **Normalize** the URL (below).
2. **Classify the address.** A search, listing or cart address is refused here,
   before any request is made (below).
3. **Fetch** it through the address-guarded client: `GET`, `text/html`
   required, 2 MiB cap, 5s total, 5 redirects maximum.
4. **Parse the whole document**, bounded by the fetch cap above.
5. **Run the chain**, merging field by field.
6. **Apply the soft-404 guard**.
7. Hand the result to the form, which fills in nothing if the result is
   suspect.

## URL normalization

Applied before storage and before fetching. Both forms are kept: `url_raw` is
exactly what was pasted, `url` is the normalized form used for duplicate
detection.

| Rule | Example |
|---|---|
| Lowercase scheme and host, drop the fragment | `HTTPS://Shop.Example/X#y` → `https://shop.example/X` |
| Rewrite AmazonSmile hosts | `smile.amazon.com/...` → `www.amazon.com/...` |
| Canonicalize Amazon product paths to `/dp/{ASIN}`, dropping everything after | `/gp/product/B0EXAMPLE1/ref=...` → `/dp/B0EXAMPLE1` |
| Strip tracking and locale parameters | `utm_*`, `ref`, `ref_`, `tag`, `psc`, `th`, `s`, `l`, `qid`, `crid`, `gclid`, `fbclid`, and others |
| Strip trailing slashes | `/products/thing/` → `/products/thing` |
| Add `https://` to a bare host | `example.com` → `https://example.com/` |

The scheme is preserved rather than forced to `https`; after a fetch, the final
URL is re-normalized, so a host that redirects to https is recorded as https
because it proved it.

Duplicate detection compares normalized URLs across every list the person can
see, and **warns without blocking**.

## Addresses that cannot be products

`extract.ClassifyNonProduct` runs on the normalized URL, before the fetch. When
it names a shape, `Service.Fetch` returns `*NotAProductError` (matchable with
`errors.Is(err, ErrNotAProductPage)`) and nothing is requested.

| Shape | What it is |
|---|---|
| `search` | A results page for terms somebody typed |
| `category` | A brand, department or category listing |
| `cart` | A cart or checkout page |

The item is still saved with whatever address was pasted — this only declines to
*look it up*. The form says which shape it was and points at
[`/help#not-a-product`](../../internal/web/templates/help.templ).

**Why before the fetch.** A listing answers `200` with a page full of products.
The tiers then either pick one of them — the confidently-wrong-item failure with
a new cause — or find nothing and hand back a blank form, which is
indistinguishable from a lookup that failed on a good link. Neither tells the
person the one thing that would help, and the second invites a retry that cannot
work.

**Why host-keyed rather than general.** Two rule sets:

- **Universal segments**, applied to every host, because they are spelled-out
  English words that mean the same thing everywhere: `search`, `cart`,
  `checkout`, `basket`.
- **Per-host segments**, for the abbreviations. `/s/`, `/b/`, `/c/`, `/pl/`,
  `/sch/`, `/browse`, `/cp/` mean listings on the hosts that define them and
  something else entirely elsewhere — `/s/some-product` is a perfectly good
  product path on a shop that has never heard of the convention.

A product segment is never in the table; that is what makes the table safe. The
home-improvement chain serves products from `/p/{slug}/{id}`, so `p` is absent
and a product address falls straight through. A wrong "that is not a product" is
worse than the blank form it replaces, because it tells somebody their good link
is bad — so an unknown host always falls through to an ordinary lookup.

**Where the cases came from.** The production log, not invention. Over two days
one family member pasted a brand listing three times in seven minutes and a
search address once with the typed terms still misspelled in the path
(`/s/Ryobi%20cordless%201gallon%20rank%20sprayer%20woth%20replacement%20tank`).
Four of the seven failed lookups in that window were addresses that could never
have worked. `TestClassifyNonProductRecognizesListings` is built from those
addresses; `TestClassifyNonProductLetsProductsThrough` is the other half and is
the more important of the two.

## Chain tiers

Run in order. Merging is **per field**, not per tier: the first tier to produce
a non-empty title wins the title even if a later tier supplies the price. The
winning tier for each field is recorded in `items.field_sources`.

| # | Tier | Applies to | Yields |
|---|---|---|---|
| 1 | Shopify JSON | paths containing `/products/{handle}` | title, description, vendor, all images, price (cheapest variant), SKU and option values when there is exactly one variant |
| 2 | JSON-LD | any page with `application/ld+json` | `name`, `description`, `image`, `offers.price`, `offers.priceCurrency`, `brand`, `sku`, `color`, `size`; `ProductGroup` adds `productGroupID` and `hasVariant` |
| 3 | Microdata | head-level `itemprop` metas | same shape as JSON-LD |
| 4 | OpenGraph | any page | `og:title`, `og:description`, `og:image:secure_url` (preferred over `og:image`), `product:price:*`, `og:type` |
| 5 | Sidecar | any page, only when configured | title, a free-text price, an image — per-site handling for marketplaces |
| 6 | Manual | always | whatever the person types |

Notes:

- Tier 1 fetches `{url}.json`. A store that answers that with HTML is not
  Shopify, and the tier declines rather than guessing.
- Tier 1 only maps variant options to attributes when a product has exactly one
  variant. With several, guessing which the recipient wants is the mistake that
  produces a wrong gift.
- Tier 3 sees the whole document, so body-level microdata is in scope.
- When a document carries several `Product` nodes — a recommendation rail is
  the usual reason — one whose `@id` or `url` matches the fetched address is
  preferred. A node without either is still used; it is only outranked.
- **`<title>` is never used as a product name.** It is SEO copy far more often
  than it is a product name.
- Tier 5 is skipped when the earlier tiers already produced both a title and a
  price.
- A price of zero is never recorded. Retailers that fetch the real number by
  script after load publish `"price": "0.00"` in the meantime, and a $0.00 item
  on a list is worse than a blank one.

## ProductGroup

A retailer selling one item in several colors or sizes usually publishes a
single `ProductGroup` node rather than a `Product`: the group carries the name,
brand, description and `productGroupID`, and each variation hangs off
`hasVariant` as a `Product` of its own.

Tier 2 accepts both types. What it takes from a group with more than one
variant is deliberately limited:

| Field | Taken from |
|---|---|
| Title, description, brand | the group |
| SKU | `productGroupID`, never a variant's `sku` — that names one color nobody chose |
| Price | the **cheapest variant that has a real one**, which is what `AggregateOffer.lowPrice` means elsewhere |
| Image | the group's, or the first variant's if the group has none |
| `color`, `size`, `material` | **nothing** — a group that `variesBy` color has no color, and picking the first listed is how a list promises gray and gets navy |

A group with exactly one variant is read as that variant, attributes included:
there is nothing to guess between.

Accepting only `@type: Product` previously meant these pages yielded no
structured fields at all. The knock-on effect was worse than the missing data:
with no structured product to corroborate it, the soft-404 guard saw
`og:type: website` and warned that a real product page was not a product page.

## Canonical follow-through

One address can have two renderings. A retailer may serve a legacy or campaign
path as a complete `200` document that carries an OpenGraph title and no
structured data whatsoever, while the slug it declares canonical carries the
full `Product` node with the price, SKU and brand. Nothing is blocked, nothing
redirects, and no work on the tiers reaches a price that was never sent.

So when a lookup comes back **without a price** and the page names a canonical,
`Service.Fetch` asks that address instead. The gate is narrow on purpose:

- Only when the price is missing. A page that gave one is not second-guessed,
  and the common case stays at one request.
- Only when the canonical **still carries the identifying segment** of the
  address asked for — the product id or listing code. Same id with a different
  slug is one product spelled two ways. A different id is a different product,
  and that stays a suggestion the owner accepts by hand (see
  [Soft-404 guard](#soft-404-guard)).
- Same host only, via `CanonicalAlternative`.
- **One hop.** The second page's own canonical is not followed, so two pages
  naming each other cannot loop.
- The result is used only if it is better: it must have a price and a title, and
  must not be blocked or suspect. Otherwise the first result stands.

When the follow-up wins, the canonical address is what gets stored — the fields
came from it, and recording one page's price against another page's URL is the
provenance mistake `field_sources` exists to prevent. The address the person
pasted is still kept as `URLRaw`.

A canonical tag is an assertion about indexing, which is why the gate above is
so hedged. A page can also make the stronger claim that this address is simply
not where the product is — see [redirects that never reach the status
line](#redirects-that-never-reach-the-status-line), which is resolved first.

## JSON-LD that is not in a script tag

A Next.js App Router page does not serve its JSON-LD as a `<script>`. It serves
a description of one. The server component tree arrives as a stream of

```js
self.__next_f.push([1,"<chunk>"])
```

calls whose chunks concatenate into a single payload, and the JSON-LD sits
inside that payload as a prop:

```js
["$","script",null,{"type":"application/ld+json",
  "dangerouslySetInnerHTML":{"__html":"{\"@context\":\"https://schema.org\"…"}}]
```

The browser turns that into a real script tag when it hydrates. Nothing that
does not execute JavaScript ever sees one — which is why tier 2 found zero
blocks on pages whose product data was complete and already in the response
body we had paid to fetch.

`internal/extract/nextflight.go` recovers it. Two details are load-bearing:

- **Concatenate before extracting.** The payload is chunked at arbitrary
  offsets, so a JSON-LD blob can straddle two pushes. A per-chunk scan silently
  loses whichever blobs land on a boundary, which presents as "that retailer
  doesn't publish structured data."
- **Two levels of escaping.** The chunk is a JSON string and the `__html` value
  inside it is a JSON string again. Both are undone with the JSON decoder;
  hand-rolled unescaping gets `\\"` and `\u` sequences wrong in ways that
  corrupt prices.

Recovered documents are appended after any real script tags, so a page that
publishes both is taken at its word. When a payload describes several products
and none of them claims this page's address, nothing is recovered — half the
fields are still available from OpenGraph, and a wrong price is not recoverable
at all, because nobody re-checks a field that looks filled in.

## Redirects that never reach the status line

The same payload can carry a redirect. A server component redirects by
throwing, and on a streamed response the `200` and the opening shell are already
sent by the time it does — there is no status line left to change. So the
redirect is written into the payload as an error digest instead:

```js
20:E{"digest":"NEXT_REDIRECT;replace;https://shop.example.com/p/real-slug/ID.html;307;"}
```

React's runtime acts on it in the browser. Nothing that does not execute
JavaScript ever leaves the first address.

Retailers keep aliases for product paths — an older slug, or one spelled with
the punctuation of the product name rather than the brand prefix the site
settled on — and an alias is exactly what this mechanism serves. Asking for one
returns a complete 1 MB `200`: navigation shell, mega-menu, dozens of payload
chunks, an unresolved Suspense boundary where the product should be, and the
digest. No title, no OpenGraph, no JSON-LD, and the string `price` not present
once in the megabyte. Every tier correctly reports nothing, the soft-404 guard
has nothing to doubt, and the form comes back blank with no explanation — while
the address in the digest carries the full `Product` node.

So `Service.Fetch` asks the payload whether it redirected, and if it did, fetches
that address instead. This runs **before** canonical follow-through, because
everything the canonical logic reasons about would otherwise be read off a page
that was never the product. The gate:

- Only when the first pass produced **no title and no price**. A payload carries
  the errors of every segment that threw, not only the page's own, so a page
  that described something is not overruled by one of them.
- Only **one destination**. Two digests naming two addresses is a guess, and
  refusing leaves a blank form, which is recoverable; landing on the wrong
  product is not.
- Same host only, via the same resolution as `CanonicalAlternative` — the digest
  is content written by the page being examined.
- **One hop.** The second page's payload is not consulted for a further
  redirect, so two aliases naming each other cannot loop.
- Whatever that address answers is adopted, **including a refusal**. The shop
  named it as the product's own, so it is the only address that can speak for
  the product, and "the shop refused" is a truer report than a blank form. A
  transport error is different — nothing was answered, so the first result
  stands.

The destination becomes the stored `url`, since that is where the fields came
from; the pasted address stays as `url_raw`.

## Soft-404 guard

Runs after the chain, over whatever it produced. This is the most important
correctness check in the extractor.

A real dead product link returned HTTP 200 with valid OpenGraph tags describing
the parent *collection* page. Every tier "succeeded" and the result was a
confident, wrong item.

The result is marked **suspect** if any of these hold:

| Signal | Condition |
|---|---|
| Wrong type | `og:type` is present and is not `product` (or `product.*`) — **and** no structured-data tier supplied the title together with a SKU or price. Plenty of storefronts emit `og:type: website` on real product pages, so on its own this signal produces false positives; a schema.org `Product` node at the requested address outweighs it |
| Redirected away | the requested path's identifying segment is absent from the final URL's path |
| Canonical disagrees | `<link rel=canonical>` also lacks that segment |
| Shape changed | a product-shaped path (`/products/`, `/product/`, `/dp/`) resolved to one that is not |
| Nothing found | no price and no SKU, on a page where a tier that normally supplies them ran and produced the title |
| Gone | HTTP 404 or 410 — marked `dead` rather than `suspect` |

### A cross-host redirect is not a structural mismatch

The path comparison above is made against **where the fetch landed**, not where it
was aimed, whenever those are on different hosts. A cross-host redirect means the
path namespace changed completely, and comparing a path in one namespace against a
path in another carries no information.

This is the common case, not a corner. A phone's share sheet hands over a shortened
link — `a.co/d/08r00ya6` for one large marketplace — whose entire purpose is to
resolve somewhere else, so the requested path can never contain the resolved path's
identifying segment. Measured on the first real corpus: **three of three short links
flagged suspect**, every one of them fine, with title, price and image read
successfully and then discarded because the guard distrusted the result. The most
common way to add an item was the one path that always warned.

Same-host redirects are unchanged, which is the case the rule exists for: a product
URL that quietly lands on a collection page. And dropping the path comparison does
not make cross-host redirects trusted — a short link that resolves to a parked
domain is still caught by the `og:type` and no-price-no-SKU signals.

## Refused requests are not suspect links

Any other status ≥ 400 — 403, 429, 5xx — means the retailer declined to serve
*this client*, which is evidence about the retailer and none at all about the
link. Those results are marked **blocked**, never suspect:

- `link_status` stays `unknown`. Nothing was learned, so nothing is recorded.
- Every field the chain scraped is discarded. Block pages carry OpenGraph tags
  of their own, and an item titled "Access Denied" is the confidently-wrong
  item with a new cause.
- The form says the shop refused and gives the status, rather than warning
  about the link.

### What "refused" turned out to mean the first time

A workwear retailer answered 403 to every request until the fetcher started
sending a browser's *whole* header set rather than `User-Agent`, `Accept` and
`Accept-Language`. It was checked with curl from the same address: three
headers gave 403, fifteen gave 200. Since curl's TLS and HTTP/2 fingerprints
are no more browser-like than Go's, that check was on the headers alone.

A department store chain refuses all of that — full header set, browser
`Accept`, brotli, and a retry carrying the cookies from its own 403 — from the
same machine whose browser loads the page fine. What it inspects is below HTTP.

One header in that set is a cliff rather than a nicety. **`Accept-Language` must
carry a value**: omitting it or sending it empty is a flat 403 from the same
filter, deterministically, on requests that are otherwise byte-for-byte the ones
it answers 200 to — measured 6 for 6 against 15 for 15. Browsers always send
one, so a request claiming to be Chrome without it contradicts itself. `fetch.New`
therefore fills the field in from `fetch.DefaultAcceptLanguage` when Options
leaves it unset, which is also the default `WISHBONE_FETCH_ACCEPT_LANGUAGE`
documents; setting that variable to a real value is supported, and there is no
way to send none.

### When a retailer inspects the handshake

`WISHBONE_FETCH_IMPERSONATE=chrome` performs the TLS handshake with Chrome's
ClientHello ([uTLS](https://github.com/refraction-networking/utls)) instead of
Go's. The department store chain above answers 200 to a request that is
otherwise identical.

That was first established with a full-fingerprint reference implementation,
before any of this was written:

```sh
docker run --rm lwthiker/curl-impersonate:0.6-chrome curl_chrome116 -sS -o /dev/null -D- '<url>'
```

and then confirmed against this fetcher directly, which is the check that
actually mattered — `curl-impersonate` matches Chrome's HTTP/2 fingerprint too,
and this does not:

```sh
wishbone check-url -impersonate chrome '<url>'   # 200, 1132379 bytes
wishbone check-url -impersonate off    '<url>'   # 403
```

The response was byte-identical to `curl-impersonate`'s. That retailer fronts
with a commercial bot-detection service which scores behavior over time and not
only the handshake, so a cold request succeeding is not a promise that a
regular polling job would.

It is **off by default**, and deliberately:

- It is an arms race. Fingerprints drift with Chrome's releases, so this needs
  attention the rest of the fetcher does not, and it will break at some point
  without warning.
- A family wishlist that types one item in by hand is not much worse off.

What it does *not* change is the address guard. Connections are still made by
the same `net.Dialer` with the same `Control` hook, so the SSRF protection sees
the resolved IP exactly as before — `TestImpersonationKeepsTheAddressGuard`
runs the whole hostile-address table in this mode for that reason. That
constraint is also why the HTTP/2 layer stays Go's: matching Chrome's SETTINGS
frames and header ordering means a fork of `net/http`, and that is not
something to put underneath the guard. Half a fingerprint is enough for that
retailer —
measured, not assumed; see the two `check-url` runs above.

Because `net/http` type-asserts connections to `*tls.Conn` for its automatic
HTTP/2 upgrade — and a uTLS connection is not one — impersonated requests go
through a small transport that tries HTTP/2 and replays over HTTP/1.1 if the
server does not negotiate it. Replaying is safe only because every request this
package makes is a bodyless GET.

To try it against one URL without turning it on anywhere:

```sh
wishbone check-url -impersonate chrome '<url>'
wishbone check-url -impersonate off    '<url>'
```

A retailer that refuses both is running a JS challenge, and the answer there is
the manual form.

So `internal/fetch` now sends client hints (`sec-ch-ua*`, only when the
User-Agent claims Chromium) and `Sec-Fetch-*` metadata matching what the
request is for — a navigation for pages, a subresource for images. A
User-Agent that does not start with `Mozilla/` gets none of it: the request's
shape follows the claim its User-Agent makes.

`Accept-Encoding` is left to `net/http`, which sends `gzip` and decompresses
transparently. Matching Chrome's full list would mean decoding brotli to gain
one header.

If a retailer still refuses, that is a fingerprint or a JS challenge, and the
answer is the manual form rather than an escalation.

The URL-shape signals are deliberately *not* softened the same way: if the
address moved, structured data on the page you landed on describes the wrong
product, which is exactly the failure this guard exists to prevent.

"Identifying segment" is the last meaningful path segment, ignoring a
marketplace's
`ref=` crumbs, and is matched as a path component. That is why a redirect from
`/dp/B0EXAMPLE1` to `/dp/B0EXAMPLE1/ref=...` is fine while
`/products/a-thing` → `/collections/best-sellers` is not.

On suspect: **nothing is auto-filled.** The reasons are shown, along with what
the page did say, and the owner decides between two buttons:

| Button | What it does |
|---|---|
| Use these details | Applies the held-back values, posting them back from the hidden fields the warning carried. No second fetch: what is applied is what was displayed. The item is stored with `link_status = suspect` regardless — accepting the values does not make the page trustworthy |
| Look up *&lt;canonical&gt;* instead | Re-runs the ordinary lookup against the address the page claims for itself, changing which link the item points at. Offered only when `CanonicalAlternative` returns one |

`CanonicalAlternative` resolves `<link rel=canonical>` against the address
actually fetched and normalizes it, then returns "" unless it is **on the same
host** and differs from what was already fetched. Same-host is a hard rule: the
canonical tag is written by the page under examination, and the re-lookup would
otherwise let it aim the fetcher anywhere.

This button is for a canonical that names a *different* product. A canonical
that names the same product at a different slug is followed automatically when
the price is missing — see [Canonical follow-through](#canonical-follow-through)
— and never reaches this prompt.

The picture found on a suspect page is described, never rendered — an `<img>`
pointed at the retailer would leak the viewer's IP, which is the whole reason
images are proxied.

`items.link_status` records `ok`, `suspect` or `dead`, submitted by the form
and re-validated on save; a status arriving without a link, or one that is not
a value the guard can produce, is stored as `unknown`. Editing an item never
changes it.

### A retailer that refuses everything

Measured 2026-08-22, on the home-improvement chain whose 403s dominate the
production log. Recorded here so it is not re-diagnosed: **there is no lookup
path for these product pages**, and the answer is the manual form.

Every one of these was tried against the same product address:

| Attempt | Result |
|---|---|
| `check-url -impersonate chrome`, from the sandbox | 403, 2259 bytes |
| `check-url -impersonate chrome`, **from the production pod** | 403, 2259 bytes |
| Sidecar tier, called directly on its own port | 403 (`Res not ok. Status: 403 Forbidden`) |
| `curl`, bare | 403 |
| `curl` with a Chrome User-Agent | 403 |
| `curl` with the full Chrome header set (client hints, `Sec-Fetch-*`, HTTP/2) | 403 |
| `facebookexternalhit`, `Twitterbot`, `Slackbot`, `WhatsApp`, `Discordbot` | 403, 476 bytes |
| `Googlebot` | 403, 476 bytes |
| `/p/svcs/frontEndModel/{id}`, `/p/{id}` | 403 |

Three things worth taking from the table:

- **It is not the egress address.** Production is a residential connection and
  the sandbox is a datacenter one; both are refused identically. A browser on
  that same residential connection opens the page.
- **It is not the fingerprint, or not only.** Chrome's ClientHello plus Chrome's
  full header set changes nothing, and the sidecar — a separate client with its
  own stack — is refused too. The provider is running a JS sensor, so the
  request that succeeds is the one that executed their script.
- **The block page differs by claimed identity** (2259 bytes for a browser UA,
  476 for a crawler), which means the classification is deliberate rather than a
  blanket rule. Declared crawlers, `Googlebot` included, are refused — real
  `Googlebot` is verified by reverse DNS from Google's own ranges, so claiming it
  from anywhere else gets nothing.

Beating a JS sensor would mean running a real browser per lookup. That is not
in scope, and the refusal path already handles this correctly: the link is
saved, nothing is claimed about it, and the person types the two fields. What
*was* worth fixing is the retrying — see
[Addresses that cannot be products](#addresses-that-cannot-be-products), which
covers the search and brand addresses from the same log, and the `/help`
wording, which now says plainly that some shops refuse every time.

## Images

- Fetched through the same guarded client. 5 MiB cap, `image/*` required.
- Decoded and **re-encoded** with the standard library rather than stored
  verbatim, which strips EXIF and neutralizes polyglot files. JPEG, PNG, GIF
  and WebP decode; PNG in stays PNG, everything else becomes JPEG.
- Stored content-addressed at `{sha[0:2]}/{sha}.{ext}`, deduplicated by hash,
  with a long-edge-1024 derivative alongside the original.
- Never hotlinked. Links rot, and hotlinking would leak every viewer's IP
  address to the retailer.
- Served only through the authenticated `/images/{sha}` handler, which checks
  the viewer can see an item referencing that blob.

## Sidecar contract

`GET {EXTRACTOR_SIDECAR_URL}/extract?url=<url-encoded>` → HTTP 200:

```json
{
  "title": "", "description": "", "price": "279.99", "currency": "USD",
  "images": ["https://..."], "image": "https://...",
  "sku": "", "brand": "", "attributes": {"size": "L"}, "error": ""
}
```

Every field is optional. A non-200, a malformed body, or a non-empty `error`
fails the tier, and a failed tier degrades to the manual path. The client
applies `EXTRACTOR_SIDECAR_TIMEOUT` and does not retry.

The sidecar must bind loopback only. Reference implementation and rationale:
[`deploy/sidecar/`](../../deploy/sidecar). It wraps `get-product-name` (MIT),
whose own output is `{ name, price, image }` with the price as free text; the
shim passes that through and the Go side parses what it can.

## Measured behavior

Results from a real cluster with a datacenter IP address, which is the only
setting that counts — bot detection keys on source address and on the client's
TLS and header fingerprint, so a laptop tells you nothing.

| Site shape | Without the sidecar | With the sidecar |
|---|---|---|
| Shopify storefront | Full success in <1s: title, price, currency, SKU, brand, all images. Tier 1 wins most fields, OpenGraph supplies the currency Shopify's JSON omits | unchanged — tier 5 is skipped, having nothing to add |
| Large marketplace | Description only. The page loads and parses, but carries no product metadata for non-browser clients | Title, price and image recovered; description still from OpenGraph. Adds ~2.3s |
| Dead product link | Correctly flagged `suspect` on three independent signals, nothing auto-filled | unchanged |
| Single-page storefront with `og:type: website` on product pages | Title, SKU, brand and image from JSON-LD; **no price**, because the storefront renders it client-side and no server-side extractor can see it. Earlier releases also flagged these pages suspect on the og:type alone — a false positive fixed by requiring corroboration | unchanged |
| Department store, legacy path for a product that also has a canonical slug | The legacy rendering is a complete 1 MB `200` with an OpenGraph title, no JSON-LD anywhere in it, and the string `price` not present once. Title only. With canonical follow-through: full price, SKU and brand from the canonical rendering, at the cost of one extra request | unchanged — the follow-up already produced a price, so tier 5 is skipped |
| Department store alias of a product path | Nothing at all: a 1 MB `200` shell whose only mention of the product is a `NEXT_REDIRECT` digest in the payload. Blank form, no warning, nothing wrong with the link. With the streamed-redirect hop: title, price, SKU, brand and image from the address the digest names, at the cost of one extra request | unchanged — the hop produces a price, so tier 5 is skipped |
| Department store publishing `ProductGroup` with a client-side price | Title, description, brand, group SKU and images from the group; **no price** — every variant is published as `"0.00"` until script fills the real one in, and that zero is refused rather than stored. Before `ProductGroup` was accepted the tier yielded nothing, and the page was wrongly warned about as not a product page | No change. Reaching this price needs a tier that runs the page's JavaScript, and the bundled wrapper's library is cheerio over `node-fetch` — HTML parsing on a plain HTTP response, with per-site rules but no browser engine |

The marketplace row is the entire argument for the sidecar, and it is a
regression against the app Wishbone replaces if you skip it.

Two things worth re-testing if extraction ever seems to degrade: whether the
site now serves an interstitial to the configured User-Agent, and whether its
structured data moved somewhere no server-side parser can see — client-side
rendering is the case no amount of parsing reaches, and the sidecar is the
answer to it.

## Testing extraction

Golden-file tests over fixtures in `internal/extract/testdata/`, including a
live/dead pair from the same fictional store. No test touches the network.

Real-world checks must be run **from inside the pod** — bot detection depends
on both source address and User-Agent, so results from anywhere else do not
transfer.
