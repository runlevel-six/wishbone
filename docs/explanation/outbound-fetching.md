# Fetching URLs people paste

Link lookup is a nice convenience and the single riskiest thing Wishbone does.
The app makes outbound HTTP requests to addresses supplied by a user, from
inside a private network, with whatever network access that network happens to
grant. That is the textbook shape of a server-side request forgery problem.

## Why a URL check is not enough

The obvious implementation validates the string:

```go
// Wrong.
if isPrivateHost(u.Hostname()) { return ErrBlocked }
```

It fails against DNS rebinding. The attacker controls the name, so it resolves
to a public address when you validate and a private one a moment later when you
dial. Nothing about the string changed. Blocklisting hostnames also invites an
endless parade of encodings — decimal IPs, `0x7f.1`, `::ffff:10.0.0.1`,
alternative unicode forms — each of which is a separate thing to get right.

## Where the check actually lives

In `net.Dialer.Control`, which the runtime calls with the **resolved address,
immediately before connecting**:

```go
dialer := &net.Dialer{
    Control: func(network, address string, _ syscall.RawConn) error {
        return guard(network, address, allowLoopback)
    },
}
```

At that point there is no name left to lie about. Whatever the hostname claimed
to be, this is the address the socket is about to reach, and it is either
allowed or the connection never happens.

Refused: loopback, RFC1918, link-local (which is what makes
`169.254.169.254` — the cloud metadata address — unreachable), CGNAT
`100.64/10`, multicast, unspecified, IPv6 ULA `fc00::/7`, IPv4-mapped IPv6
literals, 6to4 and Teredo ranges that can encapsulate a private target, and the
reserved/documentation blocks.

Because the guard is on the transport rather than in a pre-flight check, it
also covers **every redirect hop** for free. A perfectly ordinary public URL
that 302s to `http://10.0.0.1/` is stopped at the second connect, not the
first.

## What else the fetcher constrains

- 5s total, 2s to connect. A slow retailer must not tie up a request.
- 2 MiB body cap for pages, 5 MiB for images, via `io.LimitReader`.
- 64 KiB of response headers.
- 5 redirects, and only `http`/`https` on any hop.
- `text/html` required for pages; `image/*` for images.
- No proxy support — a proxy would move the connection off the guarded dialer,
  which would quietly disable everything above.
- Only the `<head>` is parsed. Product pages are routinely megabytes below the
  fold and none of it carries the metadata we want.

## Two clients, deliberately

The extraction sidecar runs on loopback, which the guard forbids. Rather than
weaken the guard, the sidecar is called with a **plain** HTTP client:

- **Guarded client** — user-supplied URLs. Refuses anything not globally
  routable.
- **Plain client** — the sidecar only, a known operator-configured address
  reached over loopback.

This asymmetry is why the sidecar gets no Kubernetes Service. It has no
authentication, it will fetch anything it is asked to, and it does its own
outbound requests without any of the above. Its containment is that nothing
outside its own pod can reach it.

## Defense in depth, clearly labeled

The deployment ships an egress `NetworkPolicy` that carves the private ranges
out of the pod's allowed destinations. It is a second line, not the first. If
you find yourself reasoning "the NetworkPolicy will catch it", that is the
moment to fix the guard instead — policies are environment-specific and a
`kubectl apply` away from being absent.

## Not hotlinking, and why it belongs here

Images are downloaded and stored rather than referenced. Two reasons, and the
second is a privacy property rather than an availability one:

1. Retailer URLs rot, and a wishlist full of broken images is a bad wishlist.
2. A hotlinked image means every family member's browser contacts the retailer.
   That leaks their IP address, their user agent, and — through the referrer,
   were it not suppressed — the fact that they are looking at that product.
   Fetching once, server-side, keeps that between the server and the shop.

Stored images are re-encoded rather than saved as received, which strips EXIF
(location data in a phone photo) and destroys polyglot files that are
simultaneously a valid image and a valid script.

## Testing it

`internal/fetch/fetch_test.go` runs a table of hostile URLs — loopback by
literal and by name, metadata, each private range, ULA, mapped, multicast,
6to4 — and requires each to fail with the guard's own error, so a DNS failure
cannot be mistaken for a successful block. A separate test stands up a local
server that redirects into RFC1918 space and asserts the redirect is refused.

The tests found a real bug during development: `net.ParseIP("1.1.1.1")` returns
a 16-byte v4-in-v6 representation, so an over-eager mapped-address check was
rejecting the entire IPv4 internet. The check now inspects the literal text
rather than the parsed value, and a test pins that public addresses still
work — a guard that blocks everything is not secure, it is broken.
