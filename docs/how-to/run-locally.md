# Run Wishbone locally

For development, review, or trying a change before it goes near real data.

## Quick start

```sh
make tools      # once: installs the templ compiler
make run
```

That serves <http://127.0.0.1:8080> with data in `./tmp`, insecure cookies (so
plain http works), and a bootstrap administrator whose username is your shell
username.

Set the bootstrap password explicitly:

```sh
WISHD_DEV_PASSWORD='at-least-ten-characters' make run
```

The bootstrap account is created only while the users table is empty. After
that the variables are ignored and everyone joins by
[invite](invite-people.md).

## Run it without make

```sh
templ generate
WISHD_ADDR=127.0.0.1:8080 \
WISHD_DATA_DIR=./tmp \
WISHD_SECURE_COOKIES=false \
WISHD_BOOTSTRAP_ADMIN=you \
WISHD_BOOTSTRAP_ADMIN_PASSWORD='at-least-ten-characters' \
go run ./cmd/wishd
```

Every variable is listed in [Configuration](../reference/configuration.md).

## Turn off outbound fetching

Link lookup makes real requests to whatever URL is pasted. To work offline, or
to keep a development instance from touching the internet at all:

```sh
WISHD_FETCH_ENABLED=false make run
```

The "Start from a link" box disappears and the manual form is the only path,
which is a supported way to run in production too.

## Start from a clean database

```sh
make clean      # removes ./tmp, ./bin and coverage output
```

Migrations run at startup, so the next start rebuilds the schema and reseeds
the categories.

## Sign in as two people at once

Owner-blindness means you frequently need two identities. Use one normal
browser window and one private window — two tabs in the same window share a
session cookie.

## Common problems

**`templ: command not found`** — `make tools`, and make sure `$(go env
GOPATH)/bin` is on your `PATH`.

**Changes to a `.templ` file do nothing** — templates compile to Go. Run
`templ generate` (or any `make` target, which does it for you).

**"Your session expired. Go back, reload the page and try again."** — a CSRF
failure. In development this usually means the server restarted and
`WISHD_SECRET_KEY` was unset, so a new random key was generated. Reload the
page. To avoid it entirely, set a fixed key:

```sh
WISHD_SECRET_KEY=$(openssl rand -hex 32) make run
```

**The browser refuses to send the session cookie** — you are serving plain http
with `WISHD_SECURE_COOKIES=true`. Set it to `false` locally.
