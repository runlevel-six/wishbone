# Configuration

All configuration is read from the environment at startup. There is no config
file. Unset variables take the default shown; an invalid duration or boolean
falls back to the default rather than failing to start.

## Server

| Variable | Default | Description |
|---|---|---|
| `WISHD_ADDR` | `:8080` | Listen address |
| `WISHD_BASE_URL` | derived from the request | Public origin, e.g. `https://example.com`. Used to build invite links. Trailing slash stripped |
| `WISHD_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. Logs are JSON on stdout |

## Storage

| Variable | Default | Description |
|---|---|---|
| `WISHD_DATA_DIR` | `/data` | Base directory for everything persistent |
| `WISHD_DB_PATH` | `$WISHD_DATA_DIR/app.db` | SQLite database file |
| `WISHD_IMAGE_DIR` | `$WISHD_DATA_DIR/images` | Content-addressed image store |

The database is opened with `journal_mode=WAL`, `foreign_keys=ON`,
`busy_timeout=5000`, `synchronous=NORMAL`, and a connection pool of exactly
one. Migrations run in-process at startup.

## Sessions and security

| Variable | Default | Description |
|---|---|---|
| `WISHD_SECRET_KEY` | random per process | 32+ hex characters (`openssl rand -hex 32`). Signs CSRF tokens. When unset, a random key is generated at startup, CSRF tokens stop validating across restarts, and the admin page warns |
| `WISHD_SECURE_COOKIES` | `true` | Sets the `Secure` cookie flag and sends HSTS. Set `false` only when serving plain http locally |
| `WISHD_SESSION_TTL` | `720h` (30 days) | Session lifetime. Slid forward on use |
| `WISHD_INVITE_TTL` | `168h` (7 days) | Invite link lifetime |
| `WISHD_TRUSTED_PROXY_CIDRS` | empty | Comma-separated CIDRs permitted to set `X-Forwarded-For` / `X-Real-IP`. Requests from anywhere else use the socket address. Empty means no header is ever trusted |

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
| `WISHD_FETCH_ENABLED` | `true` | Master switch for all outbound fetching, pages and images alike. `false` hides the link-lookup UI |
| `WISHD_FETCH_USER_AGENT` | a browser UA string | Sent with page and image requests |
| `WISHD_FETCH_ACCEPT_LANGUAGE` | `en-US,en;q=0.9` | Sent with page and image requests |
| `EXTRACTOR_SIDECAR_URL` | empty | Base URL of the extraction sidecar, e.g. `http://127.0.0.1:8081`. Empty disables tier 5 |
| `EXTRACTOR_SIDECAR_TIMEOUT` | `10s` | Per-request timeout for the sidecar. No retries |

Fixed in code, not configurable: 5s total request timeout, 2s dial timeout, 5
redirects maximum, 64 KiB response headers, 2 MiB page bodies, 5 MiB images,
`text/html` required for pages, `image/*` required for images.

## Bootstrap

| Variable | Default | Description |
|---|---|---|
| `WISHD_BOOTSTRAP_ADMIN` | empty | Username of the first administrator |
| `WISHD_BOOTSTRAP_ADMIN_PASSWORD` | empty | Its password, minimum 10 characters |

Both are consulted only when the users table is empty, and ignored on every
subsequent start. If the table is empty and they are unset, the app starts and
logs a warning; nobody can sign in until they are supplied.

## Subcommands

| Command | Description |
|---|---|
| `wishd` | Serve the application |
| `wishd hash-password` | Read a password from stdin, print an argon2id hash. For administrative password resets |
| `wishd help` | Usage summary |

## Endpoints for operators

| Path | Auth | Description |
|---|---|---|
| `/healthz` | none | Liveness. Always 200 while the process is up |
| `/readyz` | none | Readiness. 200 only if the database accepts a write |
| `/admin/health` | admin | Claim invariant check. 200 `ok`, or 500 listing drifting item IDs |
