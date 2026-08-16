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
an administrator sets a temporary password for them. The app does this itself —
there is no shell or `sqlite3` in the image to do it by hand:

```sh
kubectl -n $NS exec -it deploy/wishd -c wishd -- /wishd set-password -user sam
```

It prompts for the password on stderr (so it stays out of shell history and the
process table), sets `must_reset`, and signs out that account's other sessions.
Tell them the temporary password out of band; at sign-in they are sent straight
to **Account** and cannot go anywhere else until they choose their own.

Locally the same thing is:

```sh
go run ./cmd/wishd set-password -user sam -db ./tmp/app.db
```

`wishd hash-password` still exists if you would rather produce a hash and apply
the `UPDATE` yourself.

## Remove someone

Deleting a user cascades to their lists, items and claims. There is no
delete-user command yet, so this one still needs a shell: use a temporary pod
that mounts the data volume — see the restore procedure in
[Back up and restore](back-up-and-restore.md) for the pod spec — and run:

```sh
sqlite3 /data/app.db "DELETE FROM users WHERE username = 'sam';"
```

Take a [backup](back-up-and-restore.md) first. Their claims on *other* people's
items disappear too, which silently frees those items up for someone else to
claim — reasonable, but worth knowing before you do it in December.
