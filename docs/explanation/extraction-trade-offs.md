# Extraction trade-offs

Pasting a link and having the form fill itself in is the difference between a
wishlist people use and one they abandon. It is also where an app like this
most easily does something worse than nothing.

## The failure that shaped the design

A dead product link on a real retailer did not return 404. It returned **HTTP
200**, with well-formed OpenGraph tags — describing the parent collection page.

Every extractor tier "succeeded". A naive implementation produces an item with
a plausible title, a plausible price and a plausible picture, none of which
correspond to the thing the person meant to ask for. Nobody notices, because
nothing looks broken. Somebody buys the wrong present.

That is the failure mode worth designing against: not "extraction failed", but
"extraction confidently succeeded at the wrong thing".

## Hence the soft-404 guard

After the chain runs, a separate pass asks whether the result should be trusted
at all: does the page call itself a product, did the URL structurally shift
under us, did the canonical link disagree, did tiers that normally find a price
find nothing?

If any of that fires, **nothing is filled in.** The person is shown what was
found and what looked wrong, and types the item themselves.

An empty form costs thirty seconds. A wrong item costs a present.

## Manual entry is a first-class path, not a fallback

The add-item form works identically whether extraction ran, failed, or was
never attempted. There is no "couldn't fetch, sorry" dead end, no degraded
layout, no nagging.

This matters because extraction *will* fail — retailers block datacenter IPs,
change markup, or serve nothing useful — and if the manual path feels like a
punishment, the app feels broken every time that happens. It should feel like
the normal way to add something, because for some sites it is.

## Merging per field, not per tier

The chain does not pick a winning extractor. Each *field* takes the first
non-empty value offered, and records which tier supplied it.

Real pages are patchy in complementary ways: a Shopify store's product JSON has
the SKU, all the images and the price but no currency; the OpenGraph tags have
the currency. Per-tier merging would force a choice between them; per-field
merging takes both.

Recording the source in `items.field_sources` is what makes a future re-scrape
safe. If a person corrected the title, that field is marked `user`, and a later
automated pass can leave it alone instead of overwriting a human's judgment
with a scraper's.

## Head-only parsing

Only the `<head>` is fetched and parsed, stopping the stream at `</head>`.
Product pages are frequently megabytes of markup below the fold and none of it
carries the metadata worth having.

The trade-off is real and accepted: body-level microdata is invisible to us.
It is largely a legacy format at this point, and the cost of the alternative —
parsing megabytes of hostile HTML per paste — is not worth its remaining share.

## No inferred categories

Categories are always chosen by a person. Testing found product pages do not
carry a category signal that can be mapped reliably, and a wrong category means
wrong structured fields, which means a wrong size or a wrong format, which
means a wrong gift.

The same reasoning forbids parsing free-text notes into structured fields when
a category is applied to an existing item. An 80%-correct parse of "size L,
maybe M if it runs small" is not 80% of a good outcome. So the notes are shown
next to the new fields and the person transcribes them. Ten seconds of typing
beats a plausible mistake.

## The sidecar exists to hold a line, not to add a feature

Metadata-only extraction is a **regression** against the app Wishbone replaces
on large marketplaces, and marketplaces are a large share of real usage.
Shipping that regression would be a downgrade dressed as a rewrite.

Running the specialist library as a separate process is the compromise: it
keeps the extraction quality and keeps benefiting from upstream fixes for
individual sites, without linking another language runtime into the binary or
inheriting its licensing into this codebase.

It is treated as untrusted — short timeout, no retries, any failure degrades to
manual — and it is optional. With `EXTRACTOR_SIDECAR_URL` unset, the tier does
not exist and everything else works exactly as before.

## Warn about duplicates, do not block them

If a normalized URL already appears on a list the person can see, they are told
— and allowed to add it anyway. Two people can legitimately want the same
thing, and an app that refuses on the basis of a URL match will be wrong often
enough to be annoying. Warning is information; blocking is a policy nobody
asked for.
