# Enable link lookup and the extraction sidecar

Link lookup lets someone paste a product URL and have Wishbone fill in the
title, price and picture by reading the page itself. It is on by default and
works with no extra components.

The optional sidecar adds one more extractor tier for sites the built-in ones
cannot read.

## Turn link lookup off entirely

```yaml
- name: WISHD_FETCH_ENABLED
  value: "false"
```

The "Start from a link" box disappears; items are added by hand. Nothing else
changes, and no outbound requests are made for pages or pictures.

## What the built-in tiers cover

Without the sidecar, Wishbone reads Shopify product JSON, JSON-LD, microdata
and OpenGraph. In practice that covers most independent retailers well and
large marketplaces poorly. Details in
[Extraction](../reference/extraction.md).

## Add the sidecar

The sidecar is a second container in the same pod, listening on loopback only,
that runs a Node library specialising in sites the metadata tiers fail on.

1. Build it:

   ```sh
   cd deploy/sidecar/wrapper
   docker build -t your-registry/wishd-extractor:v1 .
   docker push your-registry/wishd-extractor:v1
   ```

2. Set the image in `deployment.yaml`, keep the `extractor` container, and make
   sure the app has:

   ```yaml
   - name: EXTRACTOR_SIDECAR_URL
     value: http://127.0.0.1:8081
   ```

3. Apply, then check it answers:

   ```sh
   kubectl exec deploy/wishd -c extractor -- \
     wget -qO- 'http://127.0.0.1:8081/healthz'
   ```

**Before you ship it, settle the license.** The library the wrapper depends on
has not been license-verified; see [`NOTICE`](../../NOTICE) and
[`deploy/sidecar/README.md`](../../deploy/sidecar/README.md). If it is
copyleft, running it as a separate unmodified process is generally fine, but it
has to be recorded.

## Do not give the sidecar a Service

It binds `127.0.0.1` on purpose. It has no authentication and will fetch any
URL handed to it, and unlike the Go fetcher it has no address guard. Reachable
only from its own pod is the containment.

## Check extraction against real URLs

Bot detection is sensitive to both IP address and User-Agent, so results from
your laptop tell you nothing about results from the cluster. Test from inside
the pod:

```sh
kubectl exec deploy/wishd -c extractor -- \
  wget -qO- 'http://127.0.0.1:8081/extract?url=https%3A%2F%2Fexample.com%2Fdp%2FB0EXAMPLE1'
```

## Tune the fetcher

| Variable | Why you would change it |
|---|---|
| `WISHD_FETCH_USER_AGENT` | A retailer blocks the default. Browser-like strings fare better than bot-like ones |
| `WISHD_FETCH_ACCEPT_LANGUAGE` | You want prices in a different locale |
| `EXTRACTOR_SIDECAR_TIMEOUT` | The sidecar is slow on some sites and you would rather wait than fall back |

Timeouts and size caps for page fetches are fixed in code, not configurable:
5s total, 2s to connect, 2 MiB per page, 5 MiB per image.

## When a lookup gets it wrong

- **"This link looks wrong"** — the soft-404 guard fired and filled in nothing
  deliberately. Check the link is still live; if it is, type the details in.
- **Wrong price or title** — edit the item. Corrections are recorded as
  user-sourced, so nothing will overwrite them later.
- **No picture** — the retailer served a format Wishbone cannot re-encode, or
  blocked the image request. Upload one from the edit form.

## What is not implemented

Nothing re-checks links on a schedule yet. `items.link_status` is written when
an item is created from a URL, and the owner sees a warning on a suspect item,
but a periodic re-check job is still outstanding.
