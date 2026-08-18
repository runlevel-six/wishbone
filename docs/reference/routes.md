# Routes

Every registered endpoint. Responses are HTML; there is no JSON API.

`Cache-Control: no-store` is set on all rendered pages. All mutating requests
require a CSRF token, sent either in the `csrf_token` form field or the
`X-CSRF-Token` header (htmx sends the header, configured once on `<body>`).

## Unauthenticated

| Method | Path | Notes |
|---|---|---|
| GET | `/healthz` | Liveness |
| GET | `/readyz` | Readiness; verifies the database is writable |
| GET | `/static/*` | Stylesheet, htmx, the one script, icons, web manifest |
| GET | `/sw.js` | Service worker. Served from the root because that is its scope; caches only `/static/` |
| GET | `/login` | Sign-in form. Redirects to `/` if already signed in |
| POST | `/login` | Rate limited per username and per address. Honors a `next` field, restricted to same-site paths |
| GET | `/register/{token}` | Invite registration form; 404 if the invite is used or expired |
| POST | `/register/{token}` | Creates the account and signs in |

## Authenticated

Any request without a valid session is redirected to `/login` (or answered with
`HX-Redirect` for htmx). A user with `must_reset` set is confined to `/account`
until they choose a password.

| Method | Path | Notes |
|---|---|---|
| GET | `/` | Dashboard: your lists and lists shared with you |
| POST | `/logout` | Deletes the session row and clears the cookie |
| GET | `/account` | Profile and password forms |
| POST | `/account/profile` | Change display name |
| POST | `/account/password` | Change password; signs out all other sessions |
| GET | `/claims` | Everything you have claimed, across lists. Opening it is what "seen" means: it clears the unread count on **Claimed** and marks the rows that count was about |
| GET | `/category-fields` | htmx partial: dynamic fields for a category |
| GET | `/share-target` | Receives a link from a phone share sheet (`url`, `text`, `title`). Routes to the add-item form, a list picker, or a prompt to make a list |
| POST | `/lists` | Create a list |
| GET | `/lists/{listID}` | View a list. 404 unless visible to you. `?sort=` reads it in another order: `manual` (default), `price-asc`, `price-desc`, `added-new`, `added-old`, `category`. Anything else falls back to `manual` |
| POST | `/lists/{listID}` | Rename, change visibility, replace the share list. Owner only |
| POST | `/lists/{listID}/delete` | Delete a list and its contents. Owner only |
| GET | `/lists/{listID}/items/new` | Add-item form. Owner only |
| POST | `/lists/{listID}/items` | Create an item. Owner only. `multipart/form-data` |
| POST | `/lists/{listID}/items/preview` | htmx partial: run the extractor over a URL. Owner only |
| POST | `/lists/{listID}/items/preview/accept` | htmx partial: apply an extraction the soft-404 guard held back, from the values the warning carried. Fetches nothing. Owner only |
| GET | `/items/{itemID}/edit` | Edit form. Owner only |
| POST | `/items/{itemID}` | Update an item. Owner only |
| POST | `/items/{itemID}/delete` | Soft-delete if claimed, hard-delete if not. Owner only |
| POST | `/items/{itemID}/move` | Reorder by one position (`dir=up`/`down`). Owner only |
| POST | `/items/{itemID}/move-to-list` | Move the item to `list_id`, which must be another list the same person owns; 404 otherwise. Claims travel with the item, and the response never varies with them. Optional `sort` is the order to return to. Owner only |
| POST | `/items/{itemID}/claims` | Claim `qty` units. **Refused for the list owner** |
| POST | `/claims/{claimID}/release` | Release your own claim |
| POST | `/claims/{claimID}/state` | `state=claimed`/`purchased`, your own claim |
| POST | `/claims/{claimID}/note` | Set the claimer note, your own claim |
| GET | `/images/{sha}` | Serve a stored image. 404 unless you can see an item that references it. `?full=1` for the original rather than the 1024px derivative |

## Admin

Admin-only routes answer 404, not 403, to non-admins.

| Method | Path | Notes |
|---|---|---|
| GET | `/admin` | People, invites, instance counters |
| GET | `/admin/health` | Claim invariant check, `text/plain` |
| POST | `/admin/invites` | Create an invite; the link is shown once |
| POST | `/admin/invites/{tokenHash}/delete` | Revoke an unused invite |
| POST | `/admin/users/{userID}/admin` | Grant or revoke admin (`admin=1`/`0`) |
| GET | `/admin/people/{userID}` | That person's lists, as the way into the reconciliation report. For the admin's own lists with inclusion off: an explanation and the switch, never the lists |
| GET | `/admin/lists/{listID}` | Full state of one list: every item, every claim, who holds it, whether it is bought, and whether `claimed_qty` agrees with the claim rows. Soft-deleted items included. Claim note text never |
| POST | `/admin/lists/mine` | Switch own-list inclusion on or off (`include=on`/`off`) for this browser session |

Admin confers no visibility into other people's lists through any ordinary page,
and no exemption from owner-blindness on their own. The **reconciliation report**
is the one deliberate exception, because owner-blindness makes "my claim is not
showing" unanswerable by anyone: it covers every list, and an admin's own lists
require a second deliberate action — the `POST` above, off again when the browser
closes, logged when switched on. An ordinary page never changes for an admin who
has switched it on.

## Status codes

| Code | Meaning here |
|---|---|
| 303 | Successful mutation, redirecting |
| 400 | Validation failed; the form is re-rendered with messages |
| 401 | Bad credentials on `/login` |
| 403 | CSRF token missing or wrong |
| 404 | Does not exist, or is not yours to see. The two are deliberately indistinguishable |
| 429 | Login rate limit |
| 500 | Unexpected failure. Also what an owner-blindness guard trip produces, since reaching that point is a bug |

Successful htmx mutations return `204` with `HX-Redirect`, or `200` with a
replacement fragment.
