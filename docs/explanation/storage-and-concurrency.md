# Storage and concurrency

Wishbone stores everything in one SQLite file and serves a group small enough
to fit in a kitchen. Nearly every storage decision follows from those two
facts.

## Why SQLite

At this scale a separate database server is a second thing to run, back up,
upgrade and monitor, in exchange for concurrency nobody needs. One file that
can be copied, opened locally, and inspected with a tool everyone already has
is worth more here than horizontal headroom.

The driver is `modernc.org/sqlite`, a pure-Go translation, not a cgo binding.
That is not a style preference: it keeps `CGO_ENABLED=0`, which keeps the
binary static, which is what allows a `scratch` container with no shell, no
libc and no package manager to patch.

## The pragmas are part of the design

```sql
PRAGMA journal_mode = WAL;      -- readers do not block the writer
PRAGMA foreign_keys = ON;       -- off by default in SQLite; cascades depend on it
PRAGMA busy_timeout = 5000;     -- wait for a lock instead of failing instantly
PRAGMA synchronous = NORMAL;    -- the right durability trade with WAL
```

They are applied as connection parameters, so every pooled connection gets
them, rather than being run once against whichever connection happened to be
first.

`foreign_keys = ON` deserves particular attention: SQLite disables foreign keys
by default, and every `ON DELETE CASCADE` in the schema is inert without it.

## One writer, on purpose

The pool is capped at a single connection. Reads serialize behind writes, which
at this scale costs nothing measurable and buys the elimination of an entire
class of `SQLITE_BUSY` behavior.

Write transactions use `BEGIN IMMEDIATE`, not the default deferred mode. A
deferred transaction takes a read lock and upgrades when it first writes; if
another writer got there in between, the upgrade fails and you have to retry
the whole thing. Taking the write lock up front turns a race into a queue.

One consequence worth remembering when adding code: a function running inside a
write transaction must not call another store method that takes its own
connection. With a pool of one, that is a deadlock. Every write helper receives
the transaction's handle and uses only that.

## The claim counter is denormalized on purpose

`items.claimed_qty` duplicates `SUM(claims.qty)`. Duplicated state is usually a
smell; here it is what makes the concurrency correct.

With the sum, claiming is read-modify-write: read the total, compare it with
the quantity, insert. Two people clicking at once both read 0, both decide
there is room, and both insert — and the last present on the list gets bought
twice.

With the counter, claiming is one conditional statement:

```sql
UPDATE items
   SET claimed_qty = claimed_qty + :n, updated_at = :now
 WHERE id = :item_id
   AND deleted_at IS NULL
   AND claimed_qty + :n <= quantity;
```

The database evaluates the condition and the update atomically. Zero rows
affected means "someone beat you to it", and no `claims` row is written. There
is no window to lose.

The cost is an invariant to maintain: every mutation of `claims` must adjust
the counter in the same transaction. That is paid for three ways — a `CHECK`
constraint that makes `claimed_qty > quantity` unrepresentable, an
`/admin/health` endpoint that reports drift, and a test assertion run after
every claim scenario in the suite, including one with two dozen goroutines
racing for a single unit.

## Block storage, not a network filesystem

The volume must be a block device. SQLite's locking depends on filesystem
semantics that network filesystems implement loosely or not at all; running a
database over NFS or a shared cluster filesystem is a well-documented way to
corrupt it. This is the one deployment requirement with no acceptable
workaround.

It follows that the deployment is single-replica with a `Recreate` strategy: a
`ReadWriteOnce` volume and a single-writer database cannot survive two pods
overlapping during a rolling update.

## Backups have to account for WAL

In WAL mode the database is not one file. A `cp` of `app.db` can capture the
main file without the write-ahead log that makes it consistent, producing a
backup that restores to a state that never existed.

`VACUUM INTO` produces a compacted, self-contained copy and is safe against a
live writer, so the backup sidecar can run on a timer without coordinating with
the app at all.

## What would have to change to outgrow this

If Wishbone ever needed multiple replicas, the order would be: move sessions
out of SQLite, move images to object storage, and only then swap the database.
None of that is close, and adding any of it now would cost real complexity to
serve a load that does not exist.
