# Data model

SQLite, one file, migrated in-process at startup. Every table is `STRICT`.

Conventions:

- **IDs** are UUIDv7 stored as `TEXT` — time-sortable, no sequence to
  coordinate.
- **Money** is integer cents in `price_cents`, never a float.
- **Timestamps** are RFC3339 `TEXT` in UTC.
- **Booleans** are `INTEGER` 0/1.

## Tables

### `users`

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | |
| `username` | TEXT UNIQUE | `COLLATE NOCASE` |
| `display_name` | TEXT | What other people see |
| `password_hash` | TEXT | argon2id encoded string |
| `is_admin` | INTEGER | |
| `must_reset` | INTEGER | Confines the user to `/account` until cleared |
| `created_at` | TEXT | |
| `claims_seen_at` | TEXT NULL | When this person last opened their own claims page; drives the unread count on **Claimed**. NULL means never, and the count then measures from each claim's own creation, so nobody is handed a badge for history. Read and written only by that person's own request — never shown to a list owner |
| `legacy_id` | TEXT NULL | For a future importer; nullable forever |

### `sessions`

| Column | Type | Notes |
|---|---|---|
| `token_hash` | TEXT PK | SHA-256 of the cookie value; the token itself is never stored |
| `user_id` | TEXT → users | `ON DELETE CASCADE` |
| `created_at`, `expires_at` | TEXT | Expiry slides on use |
| `user_agent` | TEXT NULL | Truncated to 200 characters |

### `invites`

| Column | Type | Notes |
|---|---|---|
| `token_hash` | TEXT PK | SHA-256 of the link token |
| `created_by` | TEXT → users | |
| `created_at`, `expires_at` | TEXT | |
| `used_at`, `used_by` | TEXT NULL | Single use |

Redemption creates the user and burns the invite in one transaction, so a
token cannot be spent twice by two concurrent registrations.

### `lists`

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | |
| `owner_id` | TEXT → users | `ON DELETE CASCADE` |
| `name` | TEXT | |
| `visibility` | TEXT | `private`, `all_users`, `selected` |
| `created_at`, `updated_at` | TEXT | |

### `list_shares`

`(list_id, user_id)` composite primary key. Only meaningful when visibility is
`selected`; rows are kept if visibility changes and reused if it changes back.

### `categories`

Seeded by migration, not editable in the app.

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | Fixed constants, stable across installs |
| `slug`, `label` | TEXT | |
| `sort_order` | INTEGER | |
| `field_schema` | TEXT | JSON array of field descriptors |

A field descriptor:

```json
{ "key": "size", "label": "Size", "type": "text", "required": false }
```

`type` is one of `text`, `number`, `select` (with `"options": [...]`), `color`.

Seeded categories: `general`, `clothing`, `shoes`, `books`, `toys`, `tools`,
`electronics`, `kitchen`, `outdoor`, `other`. Items default to `general`.
Category is always chosen by a person — it is never inferred from page
metadata, because product pages do not carry a reliable category signal.

### `items`

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | |
| `list_id` | TEXT → lists | `ON DELETE CASCADE` |
| `category_id` | TEXT → categories NULL | |
| `title` | TEXT | |
| `url` | TEXT NULL | Normalized |
| `url_raw` | TEXT NULL | Exactly what was pasted |
| `description`, `notes` | TEXT NULL | `notes` is owner-authored guidance |
| `price_cents` | INTEGER NULL | |
| `currency` | TEXT NULL | ISO 4217 |
| `price_seen_at` | TEXT NULL | When the price was captured |
| `quantity` | INTEGER | `>= 1` |
| `claimed_qty` | INTEGER | `>= 0` and `<= quantity`, enforced by CHECK |
| `attributes` | TEXT | JSON object, validated against the category schema |
| `field_sources` | TEXT | JSON: field → `user`, `shopify`, `jsonld`, `microdata`, `og`, `sidecar` |
| `link_status` | TEXT | `unknown`, `ok`, `suspect`, `dead` |
| `link_checked_at` | TEXT NULL | |
| `sort_order` | INTEGER | Display order; never claim-derived |
| `created_at`, `updated_at` | TEXT | `created_at` is shown on the card to everyone who can see the list, owner included — "Added today", "Added 3 weeks ago", then a plain date past a month. An importer must carry the original date over, not the import date |
| `edited_at` | TEXT NULL | Last owner edit; drives the "edited by owner" marker claimers see |
| `deleted_at` | TEXT NULL | Soft delete |
| `legacy_id` | TEXT NULL | For a future importer |

Items with claims are soft-deleted; unclaimed items are removed outright. Both
happen in one transaction so the owner cannot tell which occurred.

### `item_images`

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | |
| `item_id` | TEXT → items | `ON DELETE CASCADE` |
| `sha256` | TEXT | Storage key; blobs are shared between items |
| `mime`, `width`, `height` | | Of the stored, re-encoded image |
| `is_primary` | INTEGER | |
| `created_at` | TEXT | |

Files live at `{image_dir}/{sha[0:2]}/{sha}.{ext}` with a
`{sha}.d1024.{ext}` display derivative. A blob is only deleted when its
reference count reaches zero.

### `claims`

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | |
| `item_id` | TEXT → items | `ON DELETE CASCADE` |
| `claimer_id` | TEXT → users | `ON DELETE CASCADE` |
| `qty` | INTEGER | `>= 1` |
| `state` | TEXT | `claimed` or `purchased` |
| `note` | TEXT NULL | Claimer-authored. Visible to other claimers, **never** to the list owner |
| `created_at`, `updated_at` | TEXT | |

## Invariants

**`items.claimed_qty == SUM(claims.qty)` for that item, always.**

The denormalization exists so the overclaim check is a single conditional
statement rather than a read-modify-write. Every mutation of `claims` updates
the counter in the same transaction. `/admin/health` reports any drift, and the
test suite asserts it after every claim scenario.

**A claim never exceeds availability.** Enforced by the `WHERE` clause of the
claiming statement, inside `BEGIN IMMEDIATE`:

```sql
UPDATE items
   SET claimed_qty = claimed_qty + :n, updated_at = :now
 WHERE id = :item_id
   AND deleted_at IS NULL
   AND claimed_qty + :n <= quantity;
```

Zero rows affected means the claim lost the race; no `claims` row is written.

**Reducing quantity below `claimed_qty` is refused**, again by a `WHERE`
clause, so the handler can report the refusal without ever reading the count.
See [Owner-blindness](../explanation/owner-blindness.md) for why that matters.

## Migrations

Files in `internal/db/migrations/`, applied in filename order, each in its own
transaction, recorded in `schema_migrations`. Never edit an applied migration —
add a new one.
