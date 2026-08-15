# Extraction sidecar

Tier 5 of the extractor chain (plan §5.5). It exists because metadata-only
extraction is a regression on Amazon relative to the app Wishbone replaces, and
Amazon is a large share of real usage. Running `get-product-data` as its own
process keeps that quality without linking a Node runtime into the Go binary.

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

## Why localhost only

The sidecar is a second container in the same pod and must bind `127.0.0.1`.
It is not behind the app's authentication, and it will fetch any URL it is
handed. Do not give it a Service.

Note the asymmetry with the main app: the Go fetcher refuses to connect to
loopback and private addresses (plan §5.2), so calls to the sidecar
deliberately use a plain HTTP client rather than the guarded one. The sidecar
itself does its own outbound fetching without that guard, which is the reason
it gets no cluster-reachable network identity.

## Open item: license

**Verify before shipping** (plan §12). The parent project (Christmas
Community) is AGPL-3.0. If `get-product-data` is AGPL-3.0 too, running it as a
separate, unmodified process alongside an Apache-2.0 application is fine, but
it must be recorded in `NOTICE` at the repository root — where a placeholder
entry is already waiting. If the library turns out to be something else, update
`NOTICE` accordingly.

Nothing in this directory has been license-verified yet; `wrapper/` is a
reference implementation of the contract above, not a vetted build.

## Building

```sh
cd deploy/sidecar/wrapper
docker build -t REPLACE/wishd-extractor:latest .
```

## Checking it by hand

```sh
kubectl exec -it deploy/wishd -c extractor -- \
  wget -qO- 'http://127.0.0.1:8081/extract?url=https%3A%2F%2Fwww.amazon.com%2Fdp%2FB0EXAMPLE1'
```

Run the §11 corpus from **inside the pod**. Bot detection is IP- and
UA-sensitive, so results from a laptop do not transfer.
