# Back up and restore

Two things need backing up: the SQLite database and the image directory. Both
live on the data volume, and the backup sidecar copies both to a second volume
once a day.

## What the sidecar does

The sidecar runs the **same image as the server**, with `args: ["backup"]`.
That is deliberate: the image is a static binary on `scratch` running as an
unprivileged user, so there is no shell to script with, no package manager to
install `sqlite3` from, and no root to install it as. Since the app already
links SQLite, it issues the statement itself.

Every `BACKUP_INTERVAL` (default 24h) it:

1. writes `/backup/app-YYYY-MM-DD.db` with `VACUUM INTO`
2. writes `/backup/images-YYYY-MM-DD.tar.gz` from the image tree
3. prunes each kind to the most recent `BACKUP_KEEP` (default 14)

`VACUUM INTO` rather than a file copy, because the database runs in WAL mode: a
plain copy can capture the main database without the write-ahead log that makes
it consistent. `VACUUM INTO` is safe against a live writer and produces a
compacted, self-contained file. Each file is written to `.tmp` and renamed, so
a partially written backup never appears under a name a restore might pick up.

Pruning is not housekeeping — a backup loop quietly filling its volume is the
realistic failure mode here. Only the automatic daily files are pruned; a
backup you took by hand and named something else is left alone.

If a backup fails, the next attempt is in five minutes rather than a day. That
matters on a fresh deployment, where the database does not exist until the
server container has created it.

## Check that backups are actually happening

```sh
kubectl -n $NS logs deploy/wishbone -c backup --tail=20
kubectl -n $NS exec deploy/wishbone -c backup -- /wishbone backup -list
```

The container has no shell, so `ls` is not available — `-list` is the
substitute. You are looking for today's date and a size that is not zero. Do
this before you need it, not after.

## Take a backup right now

```sh
kubectl -n $NS exec deploy/wishbone -c backup -- /wishbone backup -once
```

## Copy a backup off the cluster

`kubectl cp` needs `tar` inside the container, which a `scratch` image does not
have. Stream it instead:

```sh
kubectl -n $NS exec deploy/wishbone -c backup -- /wishbone backup -dump latest > app-backup.db
kubectl -n $NS exec deploy/wishbone -c backup -- /wishbone backup -dump latest-images > images.tar.gz
```

Backup file names carry **UTC** dates, so `date +%F` in a shell west of
Greenwich names yesterday's file for part of every evening. `latest` sidesteps
that; use `date -u +%F` if you want to name one explicitly:

```sh
kubectl -n $NS exec deploy/wishbone -c backup -- \
  /wishbone backup -dump app-$(date -u +%F).db > app-$(date -u +%F).db
```

Do this on a schedule you can live with. Backups that never leave the machine
they are backing up are one hardware failure away from being no backups at all.

If the backup volume is `ReadWriteMany`, a separate CronJob can mount it and
ship files offsite without touching the application pod at all — which is the
main argument for putting that claim on a shared filesystem.

## Verify a backup

A backup you have not opened is a guess:

```sh
sqlite3 app-2026-11-01.db "PRAGMA integrity_check;"
sqlite3 app-2026-11-01.db "SELECT COUNT(*) FROM users;"
```

Better still, restore it into a local instance and click around:

```sh
mkdir -p ./tmp
cp app-2026-11-01.db ./tmp/app.db
WISHBONE_SECURE_COOKIES=false make run
```

Migrations apply on start, so an older backup is brought up to the current
schema by the restore itself.

## Restore

Restoring replaces the live database, so the app must not be writing while you
do it.

```sh
# 1. Stop the writer.
kubectl -n $NS scale deploy/wishbone --replicas=0

# 2. Put the file in place from a pod that mounts both volumes. Scaling to zero
#    removed the sidecars, so this needs a temporary pod with a shell.
kubectl -n $NS run wishbone-restore --rm -it --image=alpine:3.21 --restart=Never \
  --overrides='{"spec":{"containers":[{"name":"wishbone-restore","image":"alpine:3.21","stdin":true,"tty":true,
    "volumeMounts":[{"name":"data","mountPath":"/data"},{"name":"backup","mountPath":"/backup"}]}],
    "volumes":[{"name":"data","persistentVolumeClaim":{"claimName":"wishbone-data"}},
               {"name":"backup","persistentVolumeClaim":{"claimName":"wishbone-backup"}}]}}' \
  -- sh

# inside that shell:
#   rm -f /data/app.db /data/app.db-wal /data/app.db-shm
#   cp /backup/app-2026-11-01.db /data/app.db
#   tar -xzf /backup/images-2026-11-01.tar.gz -C /data/images
#   chown -R 65532:65532 /data
#   exit

# 3. Start again.
kubectl -n $NS scale deploy/wishbone --replicas=1
```

Deleting the `-wal` and `-shm` files matters: leaving a stale write-ahead log
beside a restored database is a good way to undo the restore.

With a `ReadWriteOnce` backup volume, that temporary pod has to land on the
same node as the volume; `ReadWriteMany` avoids the constraint entirely.

## What restoring costs you

A restore rolls everything back to the backup's timestamp — including claims.
If someone claimed a present in the window you are rewinding past, that claim
is gone and the item looks available again. Nobody is told. In December, say
something.
