# Wishbone documentation

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: light)" srcset="assets/logo-light.png">
    <img src="assets/logo-dark.png" alt="" width="120">
  </picture>
</p>

Documentation for Wishbone — a self-hosted wishlist app for a small, closed
group of people who buy each other presents.

These docs follow [Diátaxis](https://diataxis.fr): four kinds of documentation,
kept separate because they answer different questions. Start with whichever
column matches what you are doing.

| | **Practical steps** | **Theory** |
|---|---|---|
| **Study** | [Tutorials](#tutorials) — learn by doing | [Explanation](#explanation) — understand why |
| **Work** | [How-to guides](#how-to-guides) — get a task done | [Reference](#reference) — look something up |

## Tutorials

Start here if Wishbone is new to you. One path, followed end to end, with
nothing optional in the way.

- [Your first list and first claim](tutorials/first-list-and-first-claim.md) —
  run the app locally, create an account, build a list, and see the
  owner-blindness rule work from both sides.

## How-to guides

Recipes for a specific goal, assuming you already know roughly what you want.

- [Add items from your phone](how-to/add-from-your-phone.md) — install it, and
  wire up the share sheet on Android and iPhone
- [Sort a list and move items between lists](how-to/organize-a-list.md) — read a
  list by price, date or category, and shift an item to another list of yours
- [Run Wishbone locally](how-to/run-locally.md)
- [Deploy to Kubernetes](how-to/deploy.md)
- [Invite people and manage accounts](how-to/invite-people.md)
- [Back up and restore](how-to/back-up-and-restore.md)
- [Enable link lookup and the extraction sidecar](how-to/enable-link-lookup.md)
- [Work on the code](how-to/develop.md)

## Reference

Dry, complete descriptions. Look things up here; do not expect to be taught.

- [Configuration](reference/configuration.md) — every environment variable
- [Routes](reference/routes.md) — every HTTP endpoint and who may reach it
- [Data model](reference/data-model.md) — tables, invariants, categories
- [Extraction](reference/extraction.md) — chain tiers, the soft-404 rules, the
  sidecar contract

## Explanation

The reasoning behind the design. Read these before changing anything load
bearing.

- [Owner-blindness](explanation/owner-blindness.md) — the one rule the whole
  app is arranged around
- [Fetching URLs people paste](explanation/outbound-fetching.md) — why the SSRF
  guard lives where it does
- [Storage and concurrency](explanation/storage-and-concurrency.md) — SQLite,
  one writer, and the denormalized claim counter
- [Extraction trade-offs](explanation/extraction-trade-offs.md) — why a
  confidently wrong item is worse than an empty form

## Also in the repository

- [`README.md`](../README.md) — the short version
- [`NOTICE`](../NOTICE) — third-party licenses and one open license question
- [`deploy/`](../deploy) — Kubernetes manifests and the sidecar wrapper
