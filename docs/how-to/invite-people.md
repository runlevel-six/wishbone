# Invite people and manage accounts

Registration is invite-only. There is no public sign-up, no password reset
email, and no email of any kind — Wishbone sends nothing.

## Invite someone

1. Sign in as an administrator and open **Admin**.
2. Click **Create invite link**.
3. Copy the link that appears at the top of the page and send it however you
   normally talk to that person.

The link looks like `https://your-host/register/<token>`. Treat it as a
credential: anyone holding it can create one account.

Only a hash of the token is stored, so the link is displayed exactly once. If
you lose it before sending it, revoke it and make another.

Invites expire after seven days by default (`WISHD_INVITE_TTL`) and can only be
used once.

## Revoke an unused invite

On **Admin**, find the invite in the table and click **Revoke**. Used invites
cannot be revoked — the account already exists; remove the account instead.

## Make someone an administrator

On **Admin**, in the people table, click the button in the *Admin* column.

Administrators can create invites, revoke invites, and grant admin to others.
They **cannot** see claims on their own lists — admin is not an exemption from
[owner-blindness](../explanation/owner-blindness.md), and there is no admin
view of anyone else's claims either.

You cannot remove your own admin rights; ask another administrator, so the
instance can never end up with none.

## Reset someone's password

Normally people change their own password at **Account**, which requires the
old one.

For someone genuinely locked out there is no email to send a reset link to, so
recovery is a two-step manual job: mint a temporary password hash, then set it
along with `must_reset`, which makes the app demand a new password at their
next sign-in.

```sh
# 1. Generate a hash. The password is typed at the prompt, not passed as an
#    argument, so it stays out of shell history and the process table.
kubectl exec -it deploy/wishd -c wishd -- /wishd hash-password

# 2. Apply it. The backup sidecar is used here only because it is the container
#    with a shell and sqlite3 — the application image has neither.
kubectl exec -it deploy/wishd -c backup -- sqlite3 /data/app.db \
  "UPDATE users SET password_hash = '<hash>', must_reset = 1 WHERE username = 'sam';"
```

Tell them the temporary password out of band. At sign-in they will be sent
straight to **Account** and cannot go anywhere else until they set their own.

Locally the same thing is simply:

```sh
go run ./cmd/wishd hash-password
```

## Remove someone

Deleting a user cascades to their lists, items and claims:

```sh
kubectl exec -it deploy/wishd -c backup -- \
  sqlite3 /data/app.db "DELETE FROM users WHERE username = 'sam';"
```

Take a [backup](back-up-and-restore.md) first. Their claims on *other* people's
items disappear too, which silently frees those items up for someone else to
claim — reasonable, but worth knowing before you do it in December.
