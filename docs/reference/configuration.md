# Configuration

All configuration is read from the environment at startup. There is no config
file. Unset variables take the default shown; an invalid duration or boolean
falls back to the default rather than failing to start.

## Server

| Variable | Default | Description |
|---|---|---|
| `WISHBONE_ADDR` | `:8080` | Listen address |
| `WISHBONE_BASE_URL` | derived from the request | Public origin, e.g. `https://example.com`. Used to build invite links. Trailing slash stripped |
| `WISHBONE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. Logs are JSON on stdout |

## Storage

| Variable | Default | Description |
|---|---|---|
| `WISHBONE_DATA_DIR` | `/data` | Base directory for everything persistent |
| `WISHBONE_DB_PATH` | `$WISHBONE_DATA_DIR/app.db` | SQLite database file |
| `WISHBONE_IMAGE_DIR` | `$WISHBONE_DATA_DIR/images` | Content-addressed image store |

The database is opened with `journal_mode=WAL`, `foreign_keys=ON`,
`busy_timeout=5000`, `synchronous=NORMAL`, and a connection pool of exactly
one. Migrations run in-process at startup.

## Sessions and security

| Variable | Default | Description |
|---|---|---|
| `WISHBONE_SECRET_KEY` | random per process | 32+ hex characters (`openssl rand -hex 32`). Signs CSRF tokens. When unset, a random key is generated at startup, CSRF tokens stop validating across restarts, and the admin page warns |
| `WISHBONE_SECURE_COOKIES` | `true` | Sets the `Secure` cookie flag and sends HSTS. Set `false` only when serving plain http locally |
| `WISHBONE_SESSION_TTL` | `720h` (30 days) | Session lifetime. Slid forward on use |
| `WISHBONE_INVITE_TTL` | `168h` (7 days) | Invite link lifetime |
| `WISHBONE_TRUSTED_PROXY_CIDRS` | empty | Comma-separated CIDRs permitted to set `X-Forwarded-For` / `X-Real-IP`. Requests from anywhere else use the socket address. Empty means no header is ever trusted |

Cookies are `HttpOnly`, `SameSite=Lax`, `Path=/`, and `Secure` per the setting
above. Only a SHA-256 hash of a session token is stored.

Passwords are argon2id with `time=1`, `memory=64 MiB`, `threads=4`,
`keyLen=32`, and a 16-byte random salt, stored in the standard encoded form so
the parameters can be raised later without invalidating existing hashes.

Login is rate limited per username (8 attempts / 15 min) and per client address
(30 attempts / 15 min), in memory.

## Extraction

| Variable | Default | Description |
|---|---|---|
| `WISHBONE_FETCH_ENABLED` | `true` | Master switch for all outbound fetching, pages and images alike. `false` hides the link-lookup UI |
| `WISHBONE_FETCH_USER_AGENT` | a browser UA string | Sent with page and image requests |
| `WISHBONE_FETCH_ACCEPT_LANGUAGE` | `en-US,en;q=0.9` | Sent with page and image requests. Set it to another language if you like, but not to nothing: a filter that answers 200 to this request answers 403 to the same one without this header, so an empty value falls back to the default. See [Extraction](extraction.md#when-a-retailer-inspects-the-handshake) |
| `WISHBONE_FETCH_IMPERSONATE` | empty | `chrome` performs the TLS handshake with Chrome's ClientHello instead of Go's, for retailers that inspect it. Empty is off. An unknown value is a startup error, not a warning. See [Extraction](extraction.md#when-a-retailer-inspects-the-handshake) |
| `EXTRACTOR_SIDECAR_URL` | empty | Base URL of the extraction sidecar, e.g. `http://127.0.0.1:8081`. Empty disables tier 5 |
| `EXTRACTOR_SIDECAR_TIMEOUT` | `10s` | Per-request timeout for the sidecar. No retries |

Fixed in code, not configurable: 5s total request timeout, 2s dial timeout, 5
redirects maximum, 64 KiB response headers, 2 MiB page bodies, 5 MiB images,
`text/html` required for pages, `image/*` required for images.

## Link health

Re-checks stored links so a dead one surfaces to the list owner before somebody
tries to buy it. **Off by default**, for the same reason TLS impersonation is: a
job walking every item from one address is the traffic shape bot detection scores
hardest, and a cold request succeeding says nothing about sustained polling.

| Variable | Default | Description |
|---|---|---|
| `WISHBONE_LINK_CHECK_ENABLED` | `false` | Master switch. Requires `WISHBONE_FETCH_ENABLED`; the job logs a warning and does not start without it |
| `WISHBONE_LINK_CHECK_INTERVAL` | `24h` | Time between sweeps. Nothing is checked at startup, so a restart loop cannot become a request loop |
| `WISHBONE_LINK_CHECK_BATCH` | `20` | Items per sweep, oldest check first, never-checked first of all |
| `WISHBONE_LINK_CHECK_AGE` | `168h` (7 days) | Only re-check links nobody has looked at in this long |
| `WISHBONE_LINK_CHECK_SPACING` | `30s` | Pause between items within a sweep |

Only **404** and **410** mark a link `dead` — those are the shop saying the thing
is gone. A refusal (403, 429), a server error, a timeout or a DNS failure records
the attempt and **leaves the stored status alone**: none of them is evidence about
the link, and saying otherwise tells people their good links are broken. Items
that are soft-deleted or have no URL are skipped.

The job runs the whole extraction pipeline rather than asking for a status code,
because a dead product link often answers `200` — redirected to a collection page,
or a shell with no product on it — which only the [soft-404
guard](extraction.md#soft-404-guard) can judge.

## Backup sidecar

Read by `wishbone backup`, which runs as a second container using the same image.

| Variable | Default | Description |
|---|---|---|
| `BACKUP_DEST` | `/backup` | Where backups are written |
| `BACKUP_INTERVAL` | `24h` | Time between backups. A bare integer is read as seconds |
| `BACKUP_KEEP` | `14` | Daily backups of each kind to retain. Only files matching the automatic `app-YYYY-MM-DD.db` / `images-YYYY-MM-DD.tar.gz` names are pruned |

It also reads `WISHBONE_DATA_DIR`, `WISHBONE_DB_PATH`, `WISHBONE_IMAGE_DIR` and
`WISHBONE_LOG_LEVEL`. Every one can be overridden by a flag.

A failed backup is retried in five minutes rather than a full interval later.

## Bootstrap

| Variable | Default | Description |
|---|---|---|
| `WISHBONE_BOOTSTRAP_ADMIN` | empty | Username of the first administrator |
| `WISHBONE_BOOTSTRAP_ADMIN_PASSWORD` | empty | Its password, minimum 10 characters |

Both are consulted only when the users table is empty, and ignored on every
subsequent start. If the table is empty and they are unset, the app starts and
logs a warning; nobody can sign in until they are supplied.

## Subcommands

| Command | Description |
|---|---|
| `wishbone` | Serve the application |
| `wishbone backup` | Periodic backup loop. Flags: `-dest -interval -keep -db -images -once -list -dump` |
| `wishbone backup -once` | Take one backup and exit; non-zero on failure |
| `wishbone backup -list` | List existing backups — the substitute for `ls` in a shell-less image |
| `wishbone backup -dump FILE` | Stream one backup to stdout — the substitute for `kubectl cp`, which needs `tar`. `latest` / `latest-images` resolve to the newest of each kind, avoiding the UTC-vs-local date trap |
| `wishbone set-password -user U` | Set a temporary password, force a change at next sign-in, sign out that account's other sessions |
| `wishbone hash-password` | Read a password from stdin, print an argon2id hash |
| `wishbone help` | Usage summary |

## Endpoints for operators

| Path | Auth | Description |
|---|---|---|
| `/healthz` | none | Liveness. Always 200 while the process is up |
| `/readyz` | none | Readiness. 200 only if the database accepts a write |
| `/admin/health` | admin | Claim invariant check. 200 `ok`, or 500 listing drifting item IDs |
