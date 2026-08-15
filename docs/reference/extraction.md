# Extraction

What happens between pasting a URL and a filled-in form.

## Pipeline

1. **Normalize** the URL (below).
2. **Fetch** it through the address-guarded client: `GET`, `text/html`
   required, 2 MiB cap, 5s total, 5 redirects maximum.
3. **Parse the `<head>` only**, stopping at `</head>` or `<body>`.
4. **Run the chain**, merging field by field.
5. **Apply the soft-404 guard**.
6. Hand the result to the form, which fills in nothing if the result is
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

## Chain tiers

Run in order. Merging is **per field**, not per tier: the first tier to produce
a non-empty title wins the title even if a later tier supplies the price. The
winning tier for each field is recorded in `items.field_sources`.

| # | Tier | Applies to | Yields |
|---|---|---|---|
| 1 | Shopify JSON | paths containing `/products/{handle}` | title, description, vendor, all images, price (cheapest variant), SKU and option values when there is exactly one variant |
| 2 | JSON-LD | any page with `application/ld+json` | `name`, `description`, `image`, `offers.price`, `offers.priceCurrency`, `brand`, `sku`, `color`, `size` |
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
- Tier 3 sees only the head, since only the head is fetched. Body-level
  microdata is out of scope.
- **`<title>` is never used as a product name.** It is SEO copy far more often
  than it is a product name.
- Tier 5 is skipped when the earlier tiers already produced both a title and a
  price.

## Soft-404 guard

Runs after the chain, over whatever it produced. This is the most important
correctness check in the extractor.

A real dead product link returned HTTP 200 with valid OpenGraph tags describing
the parent *collection* page. Every tier "succeeded" and the result was a
confident, wrong item.

The result is marked **suspect** if any of these hold:

| Signal | Condition |
|---|---|
| Wrong type | `og:type` is present and is not `product` (or `product.*`) |
| Redirected away | the requested path's identifying segment is absent from the final URL's path |
| Canonical disagrees | `<link rel=canonical>` also lacks that segment |
| Shape changed | a product-shaped path (`/products/`, `/product/`, `/dp/`) resolved to one that is not |
| Nothing found | no price and no SKU, on a page where a tier that normally supplies them ran and produced the title |
| Error status | HTTP ≥ 400 — marked `dead` rather than `suspect` |

"Identifying segment" is the last meaningful path segment, ignoring Amazon's
`ref=` crumbs, and is matched as a path component. That is why a redirect from
`/dp/B0EXAMPLE1` to `/dp/B0EXAMPLE1/ref=...` is fine while
`/products/a-thing` → `/collections/best-sellers` is not.

On suspect: **nothing is auto-filled.** The reasons are shown and the person
confirms or corrects. `items.link_status` records `ok`, `suspect` or `dead`.

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

## Testing extraction

Golden-file tests over fixtures in `internal/extract/testdata/`, including a
live/dead pair from the same fictional store. No test touches the network.

Real-world checks must be run **from inside the pod** — bot detection depends
on both source address and User-Agent, so results from anywhere else do not
transfer.
