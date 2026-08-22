# Deploy to Kubernetes

The manifests in [`deploy/k8s`](../../deploy/k8s) describe a single-replica
deployment with a persistent volume, a backup sidecar, and an optional
extraction sidecar. Everything environment-specific is marked `REPLACE`.

## Before you start

You need:

- A cluster with a **block-mode** storage class. Not a network filesystem —
  see [Storage and concurrency](../explanation/storage-and-concurrency.md) for
  why this is not negotiable.
- An ingress controller terminating TLS. Either a certificate you already
  hold (a wildcard, say) or an issuer such as cert-manager — the manifests
  assume neither.
- A container registry the cluster can pull from.

## 1. Build and push the image

**If you are deploying this repository's own releases, skip this step.** CI
publishes on every `v*` tag, and you can pull what it published:

```
ghcr.io/<owner>/wishbone:v1          the release
ghcr.io/<owner>/wishbone:latest      the newest release, never the branch
ghcr.io/<owner>/wishbone:edge        the tip of master
ghcr.io/<owner>/wishbone:sha-<sha>   every build, immutable
```

The release job refuses to republish a tag that already exists, and checks that
the built binary reports the tag it was published under — so a version in a
running pod is the version in git.

Build it yourself when you are deploying a fork, or pushing to a registry of
your own:

```sh
make image IMAGE=your-registry/wishbone:v1
docker push your-registry/wishbone:v1
```

The image is a static binary on `scratch`: no shell, no package manager,
nothing to patch. Building it needs no local Go toolchain — the Dockerfile does
that in its build stage. Pass `VERSION` if you build outside a git checkout:
`.dockerignore` excludes `.git`, so `git describe` cannot run in the build
stage, and the default is `dev`.

## 2. Create the secret

```sh
kubectl create secret generic wishbone \
  --from-literal=secret-key="$(openssl rand -hex 32)" \
  --from-literal=bootstrap-admin=you \
  --from-literal=bootstrap-admin-password="$(openssl rand -base64 24)"
```

`secret-key` signs CSRF tokens. If it is missing the app still runs, but
generates a random key per process, so every restart invalidates in-flight
forms and the admin page shows a warning.

The bootstrap credentials are used only while the users table is empty. Keep
them or delete them afterwards; they are ignored either way.

See [`secret.example.yaml`](../../deploy/k8s/secret.example.yaml) if you prefer
to manage the secret declaratively.

## 3. Fill in the manifests

Search for `REPLACE` and set:

| File | What to set |
|---|---|
| `deployment.yaml` | image references, `WISHBONE_BASE_URL`, `WISHBONE_TRUSTED_PROXY_CIDRS` |
| `storage.yaml` | the block storage class name, volume sizes |
| `service-ingress.yaml` | hostname, TLS secret (see below) |
| `networkpolicy.yaml` | your ingress controller's namespace, any extra private ranges |
| `extractor-sidecar.yaml` | only if you are adding the optional sidecar |

**TLS.** An Ingress can only reference a TLS secret in its own namespace. If
you already terminate with a wildcard certificate, either copy that secret into
this namespace and name it in `spec.tls`, or delete the `tls:` block and let
the controller serve its default certificate for the host. cert-manager is
optional: add its `cluster-issuer` annotation only if you want a per-host
certificate issued. Nothing in the app depends on which you choose — HSTS and
the `Secure` cookie flag both come from `WISHBONE_SECURE_COOKIES`, not from
inspecting the certificate or `X-Forwarded-Proto`.

`WISHBONE_TRUSTED_PROXY_CIDRS` decides who may set `X-Forwarded-For`. Set it to
the range your ingress pods run in. Leaving it empty is safe — the app then
uses the socket address for rate limiting — but setting it too wide lets a
client spoof its address past the login limiter.

The extraction sidecar is not part of the default apply — it needs its own
image. Everything deploys and works without it; add it later with
[Enable link lookup](enable-link-lookup.md).

## 4. Apply

```sh
kubectl apply -k deploy/k8s
kubectl rollout status deploy/wishbone
```

## 5. Check it came up

```sh
kubectl exec deploy/wishbone -c wishbone -- true   # no shell in the image; use logs instead
kubectl logs deploy/wishbone -c wishbone
```

Probes:

- `/healthz` — the process is alive
- `/readyz` — the database accepts writes

Then sign in at your hostname as the bootstrap administrator and
[invite everyone else](invite-people.md).

## Things that are deliberate, not conservative

**`replicas: 1` and `strategy: Recreate`.** SQLite has exactly one writer and
the volume is `ReadWriteOnce`. A rolling update would briefly schedule two pods
against one database file.

**`ReadWriteOnce`, not `ReadWriteOncePod`.** The backup sidecar mounts the same
volume; `RWOP` would forbid it.

**A `/tmp` `emptyDir`.** The root filesystem is read-only, and large picture
uploads spill from memory to a temporary file.

**The egress `NetworkPolicy`.** Defense in depth only. The actual control
against fetching internal addresses is inside the app; see
[Fetching URLs people paste](../explanation/outbound-fetching.md).

## Upgrading

```sh
kubectl set image deploy/wishbone wishbone=your-registry/wishbone:v2
```

Migrations run in-process at startup and are recorded in a `schema_migrations`
table, so they apply once. With one replica there is no coordination problem —
but take a backup first anyway; `Recreate` means a brief outage regardless, so
there is no cost to being careful.

## Rolling back

Roll the image back the same way. If a migration has already applied, restore
the database from a backup as well — see
[Back up and restore](back-up-and-restore.md).
