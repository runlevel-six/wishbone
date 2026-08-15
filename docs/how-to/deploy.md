# Deploy to Kubernetes

The manifests in [`deploy/k8s`](../../deploy/k8s) describe a single-replica
deployment with a persistent volume, a backup sidecar, and an optional
extraction sidecar. Everything environment-specific is marked `REPLACE`.

## Before you start

You need:

- A cluster with a **block-mode** storage class. Not a network filesystem —
  see [Storage and concurrency](../explanation/storage-and-concurrency.md) for
  why this is not negotiable.
- An ingress controller and a way to issue TLS certificates.
- A container registry the cluster can pull from.

## 1. Build and push the image

```sh
make image IMAGE=your-registry/wishd:v1
docker push your-registry/wishd:v1
```

The image is a static binary on `scratch`: no shell, no package manager,
nothing to patch. Building it needs no local Go toolchain — the Dockerfile does
that in its build stage.

## 2. Create the secret

```sh
kubectl create secret generic wishd \
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
| `deployment.yaml` | image references, `WISHD_BASE_URL`, `WISHD_TRUSTED_PROXY_CIDRS` |
| `storage.yaml` | the block storage class name, volume sizes |
| `service-ingress.yaml` | hostname, TLS issuer |
| `networkpolicy.yaml` | your ingress controller's namespace, any extra private ranges |
| `extractor-sidecar.yaml` | only if you are adding the optional sidecar |

`WISHD_TRUSTED_PROXY_CIDRS` decides who may set `X-Forwarded-For`. Set it to
the range your ingress pods run in. Leaving it empty is safe — the app then
uses the socket address for rate limiting — but setting it too wide lets a
client spoof its address past the login limiter.

The extraction sidecar is not part of the default apply — it needs its own
image. Everything deploys and works without it; add it later with
[Enable link lookup](enable-link-lookup.md).

## 4. Apply

```sh
kubectl apply -k deploy/k8s
kubectl rollout status deploy/wishd
```

## 5. Check it came up

```sh
kubectl exec deploy/wishd -c wishd -- true   # no shell in the image; use logs instead
kubectl logs deploy/wishd -c wishd
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
kubectl set image deploy/wishd wishd=your-registry/wishd:v2
```

Migrations run in-process at startup and are recorded in a `schema_migrations`
table, so they apply once. With one replica there is no coordination problem —
but take a backup first anyway; `Recreate` means a brief outage regardless, so
there is no cost to being careful.

## Rolling back

Roll the image back the same way. If a migration has already applied, restore
the database from a backup as well — see
[Back up and restore](back-up-and-restore.md).
