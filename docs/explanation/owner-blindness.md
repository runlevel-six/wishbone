# Owner-blindness

> A list owner must never learn anything about claims on their own items.

Everything else in Wishbone is ordinary CRUD. This is the one rule the
architecture is arranged around, and it is worth understanding before changing
anything near claims.

## Why it is treated as correctness, not polish

Between authenticated family members the threat is not data theft. Everyone
here is trusted. The threat is **spoiling a surprise** — and that failure is
irreversible in a way most bugs are not. You cannot un-know that someone bought
you the thing.

So it is treated as a hard correctness requirement with tests that gate
releases, not as a UI preference implemented by hiding a `<div>`.

## Why templates are the wrong place to enforce it

The tempting design is one item template with `{{if not owner}}` around the
claim parts. It fails for the reasons every template-level access control
fails:

- Someone adds a partial for htmx and forgets the conditional.
- A counter — "2 of 3 remaining" — reads innocuous and slips through review.
- Ordering, pagination or an ETag varies with claim state, and the page leaks
  without ever rendering a claim.
- Debug output, a log line, an error message.

Each is a separate mistake, and reviewing for all of them forever is not a
plan.

## The three chokepoints

**1. The data layer refuses.** `internal/store/claims.go` is the only code in
the repository that reads the `claims` table. Every read takes a viewer:

```go
func (s *Store) ClaimsForList(ctx, listID, viewerID string) (map[string]*ItemClaims, error)
```

If that viewer owns the list, it returns `ErrOwnerBlind` and no rows. Fail
closed. A handler that asks the wrong question gets an error, not data.

**2. The type has no field for it.** `internal/view` builds one of two types:

```go
type OwnerItemView struct { ItemBase }                 // no claim fields at all
type ViewerItemView struct { ItemBase; ClaimedQty int; Claims []ClaimView; ... }
```

An owner-facing template is handed `OwnerItemView`. It cannot render claim
state, correctly or otherwise, because there is no field to reference — the
program would not compile. The template does not need to be careful; it has
nothing to be careful with.

That is the difference between *hiding* data and *not having* it. On the owner
branch the claim query is never issued in the first place.

**3. Mutations refuse.** Claim creation rejects a claimer who owns the list,
re-checking ownership and visibility inside the transaction rather than
trusting the handler.

## Admin is not exempt

There is no "admin can see everything" mode. If an administrator owns a list,
they are blind to it exactly like everyone else, and the admin page carries no
claim counters at all. An escape hatch that exists is an escape hatch someone
uses in December.

## The two tests that gate it

**Route-driven leak detection.** `TestOwnerBlindnessAcrossAllRoutes` walks the
router's *registered route table* — not a hand-maintained list — and requests
every endpoint as the list owner with claims present, asserting that no
response contains a claim canary: a claim ID, a claimer's private note, a
remaining-quantity counter, a claim-state control, a claim-scoped URL.

Driving it from the router is the point. A new endpoint is covered the day it
is added, rather than the day someone remembers to add it to a list.

**Byte-for-byte comparison.** `TestOwnerResponsesUnchangedByClaims` captures
the owner's pages, adds claims, and captures them again. The bytes must be
identical.

This is what closes the subtle vectors that a canary search cannot: an item
that quietly moves up the page, a count that ticks, a response that grows by
forty bytes. It has already caught a same-length difference during development,
which a size comparison alone would have missed.

**A liveness check on the tests themselves.** `TestClaimerSeesClaimState`
asserts that a *claimer* does see all the things the owner must not. Without
it, deleting the claim UI entirely would make every blindness test pass.

## Sorting and moving: features that had to be built around the rule

Two ordinary conveniences run straight at the vectors above, and are worth
looking at as worked examples.

**Sorting a list** is an ordering that varies — the third bullet in the list of
template-level failures. It is safe here because every key on offer is
owner-authored: a price, the date the owner added the item, a category they
chose. Nothing claim-derived is available to sort by, so the same control can be
offered on your own list and on somebody else's. To keep it that way,
`TestOwnerResponsesUnchangedByClaims` captures the owner's list in *every* sort
order, before and after claims exist, and compares the bytes. A future sort that
reached for a claim count would fail that test rather than ship quietly.

**Moving an item to another list** touches an item that may already be claimed.
It is built to be claim-blind rather than claim-aware: the store method reads no
claim rows, counts nothing, and does the same work either way, because the claims
hang off the item and travel with it for free. The confirmation is the same
sentence whether or not anyone had claimed the thing — a message that appeared
only for claimed items would be a claim indicator as surely as a counter is.

The one thing it does say is about the *destination's* visibility: moving
something onto a private list hides it from everyone. That is the owner's own
setting, not anyone's claim, so they are told about it.

**The claimed-vs-total bar** is the second bullet in that list of failures — a
"N of M claimed" counter — built on purpose, for buyers only. It is worth
reading as the case where the rule cost a feature half its scope.

The ask was a bar on every list card and every list page, so that a buyer could
see at a glance what was left *and* an owner could see whether to add more
items. The first half is free. The second half is the leak vector by name: told
that 7 of 12 are claimed, an owner knows seven presents are coming, which is the
one thing the whole app exists to withhold. Only the buyer's half shipped. An
owner's card shows the item count, as it always did.

Three things keep it that way, and the reason there are three is that this
widget is one line of arithmetic away from existing on the wrong page:

- **The types are split.** `templates.ListSummary` — the owner's card — has no
  claim fields. `templates.VisibleListSummary` embeds it and adds the progress.
  `DashboardData.Mine` is the first, `.Others` is the second, so the owner's loop
  cannot draw a bar for want of anything to draw it from. This is
  `OwnerItemView` vs `ViewerItemView` at the size of a whole list.
- **The aggregate is behind the chokepoint.** `store.ProgressForLists` takes a
  viewer, returns `ErrOwnerBlind` if any list in the batch is theirs, *and*
  excludes owned lists in the SQL. The handler could have totted the same number
  up from the item rows it already had — `items.claimed_qty` is right there — and
  that is exactly why it does not: a one-liner in a handler is a one-liner
  somebody pastes into the other loop.
- **On the list page it is derived, not queried.** `view.ListPage.Progress()`
  reads `ViewerItems`, which is empty for an owner. No extra query, and nothing
  to guard, because the data an owner's page holds cannot produce the number.

`claimCanaries` carries `claim-bar` and `still available`, so
`TestOwnerBlindnessAcrossAllRoutes` checks every owner-rendered route for the
markup and the wording, and `TestOwnerResponsesUnchangedByClaims` already
compared the owner's dashboard byte for byte across a claim landing.
`TestClaimBarIsShownToABuyer` is the counterpart that stops all of that from
being vacuous.

One detail that is easy to get wrong: it counts **items, not units**. An item
asking for three with one claim on it is still there to buy, so it counts as
available. Counting units would report a list as fuller than it is, and it would
also disagree with the dashboard, which counts live items. Removed items — the
soft-deleted ones a claimer still sees, per §3.4 — are excluded from both.

## The leaks that are accepted, and why

**Reducing quantity below the claimed count is refused.** The owner is told
"this item can't be reduced right now — try removing it instead", which reveals
exactly one bit: something is claimed. The alternative is silently corrupting
claims that already exist. One bit, deliberately spent, is better than data
loss. The refusal comes from a SQL `WHERE` clause, so the handler never holds
the count it would otherwise be tempted to mention.

**Deleting a claimed item is a soft delete.** The item vanishes from the
owner's list, and the claimer sees "the owner removed this item" so they do not
buy something nobody wants. To keep the owner from inferring anything, delete
runs both statements — soft-delete, then hard-delete-if-unclaimed —
unconditionally in one transaction. The outcome differs; the observable
behavior does not.

## If the guard ever fires

`ErrOwnerBlind` reaching an HTTP handler is a bug, not a condition. The
response is a generic 500 and the event is logged with its route, because
reaching that line means an earlier check should have prevented the call.
Nothing is disclosed, which is the safe direction to fail.
