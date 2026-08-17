# Extraction sidecar

Tier 5 of the extractor chain. It exists because metadata-only extraction is a
regression on large marketplaces relative to the app Wishbone replaces, and
those marketplaces are a large share of real usage. Running the specialist
library as its own process keeps that quality without linking a Node runtime
into the Go binary.

The library is [`get-product-name`](https://www.npmjs.com/package/get-product-name)
(MIT). Confusingly, the npm package is `get-product-name` while the repository
it is published from is `get-product-data` — the same library, and the reason
this directory used to carry the wrong dependency name.

The Go app is fully functional without it. Leave `EXTRACTOR_SIDECAR_URL` unset
and tier 5 simply is not registered.

## Contract

The Go client (`internal/extract/sidecar.go`) speaks this and nothing else:

```
GET /extract?url=<url-encoded absolute URL>
Accept: application/json
```

Response, HTTP 200:

```json
{
  "title":       "string",
  "description": "string",
  "price":       "279.99",
  "currency":    "USD",
  "images":      ["https://..."],
  "image":       "https://...",
  "sku":         "string",
  "brand":       "string",
  "attributes":  { "size": "L", "color": "green" },
  "error":       ""
}
```

Every field is optional. A non-200, a malformed body, or a non-empty `error`
makes the tier fail, and a failed tier degrades to the manual path — it never
surfaces as an error to the person adding an item.

The client sets a timeout (`EXTRACTOR_SIDECAR_TIMEOUT`, default 10s) and does
**not** retry on 5xx. The sidecar is treated as untrusted.

The library returns `{ name, price, image }` — a free-text price string such as
`"279.99 USD"` or `"Currently unavailable."`. The shim passes it through
untouched; parsing is the Go side's job, and an unparseable price simply
produces no price rather than a wrong one.

## Why localhost only

The sidecar is a second container in the same pod and must bind `127.0.0.1`.
It is not behind the app's authentication, and it will fetch any URL it is
handed. Do not give it a Service.

Note the asymmetry with the main app: the Go fetcher refuses to connect to
loopback and private addresses, so calls to the sidecar
deliberately use a plain HTTP client rather than the guarded one. The sidecar
itself does its own outbound fetching without that guard, which is the reason
it gets no cluster-reachable network identity.

## License

MIT, Copyright 2020 Sam Wing — verified against both the npm metadata and the
repository's LICENSE file, and recorded in [`NOTICE`](../../NOTICE).

The parent project the library was written for is AGPL-3.0; the library is not.
It is invoked as a separate process over HTTP and is not linked into the Go
binary in any case.

## Building

```sh
cd deploy/sidecar/wrapper
docker build -t REPLACE/wishbone-extractor:latest .
```

## Checking it by hand

```sh
kubectl exec -it deploy/wishbone -c extractor -- \
  wget -qO- 'http://127.0.0.1:8081/extract?url=https%3A%2F%2Fwww.marketplace.example%2Fdp%2FB0EXAMPLE1'
```

Run the §11 corpus from **inside the pod**. Bot detection is IP- and
UA-sensitive, so results from a laptop do not transfer.
