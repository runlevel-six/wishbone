# Work on the code

## Layout

```
cmd/wishbone            entry point, bootstrap admin, janitor, hash-password
internal/config      environment configuration
internal/db          SQLite open, pragmas, embedded migrations
internal/model       entity types, IDs, sentinel errors
internal/store       all SQL; claims.go is the owner-blindness chokepoint
internal/view        OwnerItemView vs ViewerItemView — the second chokepoint
internal/auth        argon2id, tokens, CSRF, rate limiting
internal/fetch       the address-guarded HTTP client
internal/extract     URL normalization, extractor chain, soft-404 guard
internal/imgstore    fetch, re-encode, content-address, resize
internal/categories  per-category field schema and validation
internal/web         router, middleware, handlers, templates, static assets
```

Dependencies point one way: `web` → `view` → `store` → `model`. Nothing below
`web` knows about HTTP, and only `store` writes SQL.

## The loop

```sh
make check    # gofmt, vet, and the full test suite
make race     # the claim concurrency tests under the race detector
make cover    # coverage summary
```

Templates are compiled, not interpreted: `.templ` files become `_templ.go`
files via `templ generate`, which every `make` target runs first. Generated
files are committed so a plain `go build` works without the tool.

## Adding an HTTP endpoint

1. Register it in `internal/web/server.go`.
2. Write the handler in the matching `handlers_*.go`.
3. Run the tests.

Step 3 is not a formality. `TestOwnerBlindnessAcrossAllRoutes` walks the
router's registered routes, so your new endpoint is exercised as a list owner
automatically and will fail the build if it leaks claim state. That is the
intended way to find out — see
[Owner-blindness](../explanation/owner-blindness.md).

## Adding a field to items

1. Add a migration file in `internal/db/migrations/`. Never edit an applied
   one; the runner records what it has applied by filename.
2. Add the column to `itemCols` and the scan in `internal/store/items.go`.
3. Add it to `ItemBase` in `internal/view` if it should be rendered.
4. Decide, explicitly, whether it is visible to the list owner. If it can be
   influenced by a claim in any way, it belongs on `ViewerItemView` only.

## Adding an extractor tier

Implement `extract.Extractor` — `Name`, `Applies`, `Extract` — and add it to
the chain in `extract.NewService`, in priority order. Merging is per field, so
a tier that only knows how to find prices is perfectly useful.

If the tier is expensive (another process, a model, a paid API), also implement
`Fallback() bool` so it is skipped when the cheap tiers already produced a
title and a price.

Add a golden-file test with a fixture in `internal/extract/testdata/`. No test
in this repository may touch the network.

## Adding a category

Categories are seeded by migration, not managed in the UI. Add an `INSERT` in a
new migration file with a fixed UUID, a slug, a label, a sort order, and a JSON
field schema. Field types are `text`, `number`, `select` (with `options`) and
`color`.

Attributes are validated against that schema on every write, and unknown keys
are rejected rather than stored.

## Testing conventions

- Tests use a real SQLite database in a temp directory, not a mock. The claim
  invariant is enforced by SQL, so testing against anything else would test
  the wrong thing.
- `internal/web/harness_test.go` builds a whole server with two users, a list,
  items and claims. Prefer extending it to building a new fixture.
- Canary strings (`canaryNote`, `canaryClaimer`) are how leak tests detect
  claim data. If you add a new way to display claims, add a canary for it.
- `TestClaimerSeesClaimState` exists to keep the blindness tests honest: it
  fails if the claim UI disappears, which would otherwise make every leak test
  pass vacuously.

## Before opening a change

```sh
make check
```

And if you touched claims, storage or the fetcher:

```sh
make race
```

`make check` runs `gofmt -w`, which is what you want locally and the wrong thing
in CI — it would reformat the code and then pass. So CI checks rather than fixes:
unformatted Go fails the run, and so does a commit whose `*_templ.go` files do not
match a fresh `templ generate`. It also runs the linter, which `make check` does
not:

```sh
golangci-lint run ./...
```

The configuration is `.golangci.yaml`. Two settings there are worth knowing before
you fight them: `misspell` is set to US English deliberately, and the `revive`
`exported` rule is deliberately **off** — everything here lives under `internal/`,
so there is no consumer whose API those doc comments would document.

## Releasing

The version is not typed anywhere. `make` derives it from the nearest `v*` tag:

```make
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --always --dirty ...)
```

It ends up in three places that matter: the startup log, `wishbone version`, and
the query string on every `/static/` URL — which is what makes a release retire
the previous release's cached assets (see
[configuration](../reference/configuration.md)). So the tag is not bookkeeping; it
is the thing that makes a deploy reach anyone.

```sh
git tag -s v0.6.0 -m "what changed"
git push origin v0.6.0
make image            # stamps v0.6.0 from the tag
# push the image, then bump newTag in your overlay and apply
```

Tag before you build. Building first stamps `<sha>-dirty` or the previous tag plus
a commit count, and then the running version does not correspond to anything you
can check out. The `--match 'v[0-9]*'` is load-bearing for the same reason: without
it any other tag in the repository becomes the version string.
