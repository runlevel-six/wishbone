# wishd — Wishbone

A self-hosted wishlist app for a small, closed group of people who buy each
other presents. Everyone has an account; nothing is public.

The repo, module, binary and Kubernetes objects are all `wishd`. Everything a
person reads in the app says **Wishbone**.

## What it does

- Invite-only accounts, server-side sessions, argon2id passwords.
- Lists that are private, shared with everyone, or shared with named people.
- Items with quantity, notes, pictures, prices and per-category details.
- Claiming, partial claiming, releasing, and marking things bought.
- **Owner-blindness**: a list owner learns nothing about claims on their own
  items — not hidden in the interface, absent from the data the page is built
  from.
- Optional link lookup: paste a product URL and Wishbone reads the page itself,
  through a fetcher hardened against SSRF, and never hotlinks the picture.

## Try it in ten minutes

```sh
make tools
WISHD_DEV_PASSWORD=localdevpassword make run
```

Then follow [Your first list and first
claim](docs/tutorials/first-list-and-first-claim.md), which walks through
building a list, claiming from it as a second person, and watching the owner's
page stay identical.

## Documentation

Full documentation is in [`docs/`](docs/README.md), organised as
[Diátaxis](https://diataxis.fr):

- **[Tutorials](docs/tutorials/first-list-and-first-claim.md)** — learn the app
  by using it
- **How-to guides** — [run locally](docs/how-to/run-locally.md),
  [deploy](docs/how-to/deploy.md), [invite people](docs/how-to/invite-people.md),
  [back up](docs/how-to/back-up-and-restore.md),
  [enable link lookup](docs/how-to/enable-link-lookup.md),
  [work on the code](docs/how-to/develop.md)
- **Reference** — [configuration](docs/reference/configuration.md),
  [routes](docs/reference/routes.md),
  [data model](docs/reference/data-model.md),
  [extraction](docs/reference/extraction.md)
- **Explanation** — [owner-blindness](docs/explanation/owner-blindness.md),
  [outbound fetching](docs/explanation/outbound-fetching.md),
  [storage and concurrency](docs/explanation/storage-and-concurrency.md),
  [extraction trade-offs](docs/explanation/extraction-trade-offs.md)

## Built with

Go, `chi`, [templ](https://templ.guide) and htmx, over SQLite via a pure-Go
driver. No npm, no build step for the frontend, no framework. The result is a
single static binary in a `scratch` container.

## Status

Implemented: foundation, claims, extraction, categories — the full must-ship
scope and then some.

Not implemented yet:

- An importer for data from a previous wishlist app. The schema was written
  first, deliberately, so the old data model could not shape it; the columns it
  will need already exist.
- PWA manifest and share-target, so items can be added from a phone's share
  sheet.
- A periodic link-health job. Link status is recorded when an item is created
  from a URL; nothing re-checks it on a schedule.
- An optional local-LLM extraction tier.

Open questions are tracked in [`NOTICE`](NOTICE) (one unresolved third-party
license) and in the docs where they apply.

## Development

```sh
make check    # gofmt, vet, tests
make race     # claim concurrency under the race detector
```

See [Work on the code](docs/how-to/develop.md).

## License

Apache 2.0. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
