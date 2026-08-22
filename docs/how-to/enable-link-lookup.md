# Enable link lookup and the extraction sidecar

Link lookup lets someone paste a product URL and have Wishbone fill in the
title, price and picture by reading the page itself. It is on by default and
works with no extra components.

The optional sidecar adds one more extractor tier for sites the built-in ones
cannot read.

## Turn link lookup off entirely

```yaml
- name: WISHBONE_FETCH_ENABLED
  value: "false"
```

The "Start from a link" box disappears; items are added by hand. Nothing else
changes, and no outbound requests are made for pages or pictures.

## What the built-in tiers cover

Without the sidecar, Wishbone reads Shopify product JSON, JSON-LD, microdata
and OpenGraph. Measured against a real cluster: independent retailers extract
completely in under a second, while large marketplaces yield a description and
nothing else. Adding the sidecar recovers title, price and image on the
marketplace case at a cost of roughly two seconds per lookup. Full table in
[Extraction](../reference/extraction.md).

## Add the sidecar

The sidecar is a second container in the same pod, listening on loopback only,
running [`get-product-name`](https://www.npmjs.com/package/get-product-name)
(MIT) — a library with per-site handling for the marketplaces the metadata
tiers cannot read.

It is opt-in: the default manifests do not include it, because it needs a
second image built and pushed first.

1. Get the image. CI publishes it as `ghcr.io/<owner>/wishbone-extractor`,
   versioned from `deploy/sidecar/wrapper/package.json` and built whenever
   anything in that directory changes — so for this repository's own releases
   there is nothing to build.

   To build it yourself, for a fork or your own registry:

   ```sh
   cd deploy/sidecar/wrapper
   docker build -t your-registry/wishbone-extractor:v1 .
   docker push your-registry/wishbone-extractor:v1
   ```

   Note that the wrapper has no lockfile and depends on a `^` range, so two
   builds of the same source can differ. That is why its version is bumped
   rather than reused, and why the publish job refuses to overwrite a version
   that already exists.

2. Set the image in `deploy/k8s/extractor-sidecar.yaml`, then uncomment the
   `patches` entry in `kustomization.yaml`. That patch adds both the container
   and `EXTRACTOR_SIDECAR_URL`.

3. `kubectl apply -k deploy/k8s`, then check it answers:

   ```sh
   kubectl exec deploy/wishbone -c extractor -- \
     wget -qO- 'http://127.0.0.1:8081/healthz'
   ```

The library's license is MIT and is recorded in [`NOTICE`](../../NOTICE). Note
that the npm package is `get-product-name` even though its repository is called
`get-product-data`.

## Do not give the sidecar a Service

It binds `127.0.0.1` on purpose. It has no authentication and will fetch any
URL handed to it, and unlike the Go fetcher it has no address guard. Reachable
only from its own pod is the containment.

## Check extraction against real URLs

Bot detection is sensitive to both IP address and User-Agent, so results from
your laptop tell you nothing about results from the cluster. Test from inside
the pod:

```sh
kubectl exec deploy/wishbone -c extractor -- \
  wget -qO- 'http://127.0.0.1:8081/extract?url=https%3A%2F%2Fexample.com%2Fdp%2FB0EXAMPLE1'
```

## Tune the fetcher

| Variable | Why you would change it |
|---|---|
| `WISHBONE_FETCH_USER_AGENT` | A retailer blocks the default. A browser-like string also switches on the rest of a browser's header set — client hints and `Sec-Fetch-*` — because sending three headers under a Chrome User-Agent is itself what some retailers reject |
| `WISHBONE_FETCH_ACCEPT_LANGUAGE` | You want prices in a different locale |
| `EXTRACTOR_SIDECAR_TIMEOUT` | The sidecar is slow on some sites and you would rather wait than fall back |
| `WISHBONE_FETCH_IMPERSONATE` | A retailer refuses even a full browser header set. `chrome` matches Chrome's TLS handshake — see [Extraction](../reference/extraction.md#when-a-retailer-inspects-the-handshake) for why it is off by default |

## Decide whether impersonation would help

Try it on one URL before turning it on for the deployment:

```sh
kubectl exec deploy/wishbone -c wishbone -- /wishbone check-url -impersonate chrome '<url>'
```

If that turns a 403 into a 200, set `WISHBONE_FETCH_IMPERSONATE=chrome` in your
deployment. If it does not, the retailer is running a JS challenge and no
setting here reaches it.

Timeouts and size caps for page fetches are fixed in code, not configurable:
5s total, 2s to connect, 2 MiB per page, 5 MiB per image.

## When a lookup gets it wrong

- **"This link looks wrong"** — the soft-404 guard fired and filled in nothing
  deliberately. Read the reasons and what the page said, then either **Use
  these details**, or take the canonical address it offers if the page named
  one, or type the details in. An item added this way keeps `link_status =
  suspect`, so the list keeps saying so.
- **The canonical address names a different product** — normal on marketplaces
  that collapse every size and color onto one indexed listing. Take the other
  address when it is the same thing under a tidier URL; leave it when which
  variant you get matters, since that is a different purchase.
- **"That shop would not let Wishbone read the page"** — the retailer answered
  403 or similar. Nothing is wrong with the link and nothing is recorded
  against it; type the details in. Confirm with `check-url`, which prints the
  HTTP status. Some large chains refuse every request this app makes by
  default, from an address whose browser loads the page fine. Trying
  `check-url -impersonate chrome` on the same URL says whether
  `WISHBONE_FETCH_IMPERSONATE=chrome` would change that — for at least one such
  retailer it does.
- **Wrong price or title** — edit the item. Corrections are recorded as
  user-sourced, so nothing will overwrite them later.
- **No picture** — the retailer served a format Wishbone cannot re-encode, or
  blocked the image request. Upload one from the edit form.

## What is not implemented

Nothing re-checks links on a schedule yet. `items.link_status` is written when
an item is created from a URL, and the owner sees a warning on a suspect item,
but a periodic re-check job is still outstanding.
