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

If any of that fires, **nothing is filled in automatically.**

## Refusing to fill in is not the same as refusing to show

The first version of the guard threw the extraction away and told the person
the form was empty — while the server had the title and price in hand. That
overshoots. What makes a wrong item expensive is that nothing on screen
disagrees with it; a value shown *under a warning explaining what looked
wrong* has no such problem.

So the warning shows what the page said and offers to apply it, and the item
keeps its `suspect` mark either way. The machine still refuses to be confident.
The person is allowed to be, having seen the evidence.

The same reasoning decides what to do with a canonical address that names a
different product — the common case on marketplaces, where every size and color
of one garment is collapsed onto whichever listing is indexed. Following it
automatically is exactly the confidently-wrong-item failure in a new costume:
the sibling may be a different size, and the details would then describe one
product while the saved link points at another. So the address is offered as a
second lookup the owner can choose, and never as data.

An empty form costs thirty seconds. A wrong item costs a present. A withheld
fact the owner could have judged costs trust in the warning itself — which is
what makes the next warning get clicked through.

## Being refused is not a verdict on the link

Some retailers answer a datacenter address with 403 whatever it asks for. That
is a fact about the retailer. The first version treated any error status as a
dead link, so a perfectly good URL came back marked dead with a warning telling
the person to go check it — sending them to inspect the one part of the
situation that was never wrong.

Blocked and suspect are now separate outcomes. Suspect means *the page said
something and we doubt it*; blocked means *the page said nothing to us*. Only
the first is evidence about a link.

Escalating past that is a decision, not a default. Matching a browser's TLS
fingerprint, driving a headless browser, or renting residential addresses each
buy a few more sites for a while, and all three are a race against people who
do this full time. Losing that race quietly — extraction that works until it
doesn't — is worse than a form that says plainly who refused and lets you type.

So the one escalation that is implemented, `WISHBONE_FETCH_IMPERSONATE=chrome`,
ships switched off. Turning it on is someone deciding a particular shop is
worth the maintenance, having checked with `check-url -impersonate chrome` that
it would actually help. The two escalations that are not implemented are the
two that would have cost more than they bought: a headless browser executes
untrusted JavaScript from arbitrary pasted URLs, and residential proxy pools
are usually assembled from people who did not meaningfully agree to be in them.

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

## Whole-document parsing

This used to be head-only parsing, stopping the stream at `</head>` on the
reasoning that product pages are megabytes of markup below the fold and none of
it carries the metadata worth having.

The second half of that turned out to be false, and the reasoning had a hidden
assumption inside it: that a document's metadata is in its head.

On a framework that streams metadata, it is not. React emits `<title>`,
`<meta>` and `<link rel=canonical>` inline in the *body* as the response
streams, and hoists them into the head only when it hydrates. A Next.js App
Router product page therefore has a stub head and all of its metadata near the
end of the document. On the page that prompted this — a live retail product
page returning a perfectly ordinary 200 — the head was 16KB and every piece of
metadata sat at byte 1,083,000 of 1,132,379. Head-only parsing extracted
literally nothing from it, and every tier was correct to report nothing,
because nothing is what it was shown.

The cost of parsing the rest was also smaller than it looked. The document is
capped at `MaxPageBytes` by the fetcher before the parser runs, so stopping at
`<body>` never avoided fetching anything; it only skipped part of a buffer
already in memory. Tokenizing the remainder is milliseconds.

What the old stop *was* quietly buying is the part worth naming, because it had
to be paid for explicitly:

- **A recommendation rail's JSON-LD is now in scope.** "You may also like" ships
  Product nodes as well-formed as the page's own, and taking one yields a
  complete, confident, wrong item. So a Product node whose `@id` or `url` is
  this page's address is preferred over one that is not. Nothing is discarded
  for lacking an `@id` — plenty of honest pages omit it — but when the document
  describes several products and one of them claims to be this page, that one
  wins.
- **`<title>` inside inline SVG.** Icon markup carries `<title>` for screen
  readers, and a storefront body has dozens. Only the first `<title>` outside
  an `<svg>` is taken.
- **First value wins, not last.** With the body in scope, the head has to win
  on its merits rather than by being the only thing read.

Body-level microdata, previously listed here as an accepted loss, is no longer
one.

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
