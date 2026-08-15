# Back up and restore

Two things need backing up: the SQLite database and the image directory. Both
live on the data volume, and the backup sidecar copies both to a second volume
once a day.

## What the sidecar does

[`deploy/k8s/backup-script.yaml`](../../deploy/k8s/backup-script.yaml) runs a
loop that, every `BACKUP_INTERVAL_SECONDS` (default 24h):

1. `sqlite3 /data/app.db "VACUUM INTO '/backup/app-YYYY-MM-DD.db'"`
2. archives `/data/images` to `/backup/images-YYYY-MM-DD.tar.gz`
3. prunes both to the most recent `BACKUP_KEEP` (default 14)

`VACUUM INTO` is used rather than `cp` because the database runs in WAL mode: a
plain file copy can capture the main database without the write-ahead log that
makes it consistent. `VACUUM INTO` is safe against a live writer and produces a
compacted, self-contained file.

The prune is not decoration. A backup loop quietly filling the volume is the
realistic failure mode for this kind of setup, which is also why backups go to
their own claim rather than sharing the application's.

## Check that backups are actually happening

```sh
kubectl logs deploy/wishd -c backup --tail=20
kubectl exec deploy/wishd -c backup -- ls -lh /backup
```

You are looking for today's date and a file size that is not zero. Do this
before you need it, not after.

## Take a backup right now

```sh
kubectl exec deploy/wishd -c backup -- sh -c \
  "sqlite3 /data/app.db \"VACUUM INTO '/backup/app-manual-\$(date +%F-%H%M).db'\""
```

## Copy a backup off the cluster

```sh
kubectl cp wishd-pod:/backup/app-2026-11-01.db ./app-2026-11-01.db -c backup
```

Do this on a schedule you can live with. Backups that never leave the machine
they are backing up are one hardware failure from being no backups at all.

## Verify a backup

A backup you have not opened is a guess:

```sh
sqlite3 app-2026-11-01.db "PRAGMA integrity_check;"
sqlite3 app-2026-11-01.db "SELECT COUNT(*) FROM users, lists, items;"
```

Better still, restore it into a local instance and click around:

```sh
mkdir -p ./tmp
cp app-2026-11-01.db ./tmp/app.db
WISHD_SECURE_COOKIES=false make run
```

Migrations apply on start, so an older backup is brought up to the current
schema by the restore itself.

## Restore

Restoring means replacing the live database, so the app must not be writing
while you do it.

```sh
# 1. Stop the writer.
kubectl scale deploy/wishd --replicas=0

# 2. Put the file in place. Use a pod that mounts the data volume — for
#    example a temporary one, since scaling to zero removed the sidecars.
#    Replace <claim> with wishd-data / wishd-backup.
kubectl run wishd-restore --rm -it --image=alpine:3.21 --restart=Never \
  --overrides='{"spec":{"containers":[{"name":"wishd-restore","image":"alpine:3.21","stdin":true,"tty":true,
    "volumeMounts":[{"name":"data","mountPath":"/data"},{"name":"backup","mountPath":"/backup"}]}],
    "volumes":[{"name":"data","persistentVolumeClaim":{"claimName":"wishd-data"}},
               {"name":"backup","persistentVolumeClaim":{"claimName":"wishd-backup"}}]}}' \
  -- sh

# inside that shell:
#   rm -f /data/app.db /data/app.db-wal /data/app.db-shm
#   cp /backup/app-2026-11-01.db /data/app.db
#   tar -xzf /backup/images-2026-11-01.tar.gz -C /data
#   chown -R 65532:65532 /data
#   exit

# 3. Start again.
kubectl scale deploy/wishd --replicas=1
```

Deleting the `-wal` and `-shm` files matters: leaving a stale write-ahead log
next to a restored database is a good way to undo the restore.

## What restoring costs you

A restore rolls everything back to the backup's timestamp — including claims.
If someone claimed a present in the window you are rewinding past, that claim
is gone and the item looks available again. Nobody is told. In December, say
something.
