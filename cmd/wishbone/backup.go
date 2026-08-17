package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// backupCmd is the backup sidecar, as a subcommand rather than a shell script.
//
// It runs in the same image as the server, which is the point: the image is a
// static binary on scratch running as an unprivileged user, so there is no
// shell to script with, no package manager to install sqlite3 from, and no
// root to install it as. Since the app already links SQLite, the backup can
// simply issue the statement itself.
//
// VACUUM INTO is what makes this safe against a live writer. Copying the
// database file with cp would risk capturing the main file without the
// write-ahead log that makes it consistent.
func backupCmd(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	var (
		dbPath   = fs.String("db", env("WISHBONE_DB_PATH", filepath.Join(env("WISHBONE_DATA_DIR", "/data"), "app.db")), "database file to back up")
		imageDir = fs.String("images", env("WISHBONE_IMAGE_DIR", filepath.Join(env("WISHBONE_DATA_DIR", "/data"), "images")), "image directory to archive")
		dest     = fs.String("dest", env("BACKUP_DEST", "/backup"), "directory to write backups to")
		interval = fs.Duration("interval", envDuration("BACKUP_INTERVAL", 24*time.Hour), "time between backups")
		keep     = fs.Int("keep", envInt("BACKUP_KEEP", 14), "how many daily backups of each kind to retain")
		once     = fs.Bool("once", false, "take one backup and exit")
		list     = fs.Bool("list", false, "list existing backups and exit")
		dump     = fs.String("dump", "", "write one backup file to stdout and exit; \"latest\" or \"latest-images\" resolve to the newest of that kind")
		verify   = fs.String("verify", "", "open a backup and report on it, then exit; accepts a file in -dest, an absolute path, or \"latest\"")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// The container this runs in has no shell, so `ls` and `kubectl cp` are not
	// available to inspect or retrieve backups. These two flags stand in for
	// them: `-list` to see what exists, `-dump` to stream one file to stdout.
	if *list {
		return listBackups(*dest, os.Stdout)
	}
	if *dump != "" {
		return dumpBackup(*dest, *dump, os.Stdout)
	}
	if *verify != "" {
		return verifyBackup(*dest, *verify, os.Stdout)
	}

	log := newLogger(env("WISHBONE_LOG_LEVEL", "info")).With(slog.String("component", "backup"))

	if err := os.MkdirAll(*dest, 0o755); err != nil {
		return fmt.Errorf("backup destination: %w", err)
	}

	// run reports whether everything succeeded, so a failure can be retried
	// soon rather than a day later. The cold-start case is the reason: on a
	// fresh volume the database does not exist until the server container has
	// opened it, and waiting a full interval after that would leave a new
	// deployment with no backup for a day.
	run := func() bool {
		ok := true
		stamp := time.Now().UTC().Format("2006-01-02")
		if err := backupDatabase(*dbPath, filepath.Join(*dest, "app-"+stamp+".db")); err != nil {
			log.Error("database backup failed", slog.Any("err", err))
			ok = false
		} else {
			log.Info("database backed up", slog.String("file", "app-"+stamp+".db"))
		}
		switch err := archiveImages(*imageDir, filepath.Join(*dest, "images-"+stamp+".tar.gz")); {
		case errors.Is(err, errNoImages):
			// Not a failure, but say so rather than claiming an archive that
			// does not exist.
			log.Info("no images stored yet, nothing archived")
		case err != nil:
			log.Error("image archive failed", slog.Any("err", err))
			ok = false
		default:
			log.Info("images archived", slog.String("file", "images-"+stamp+".tar.gz"))
		}
		// Pruning is not housekeeping, it is the difference between a backup
		// job and a disk-filling job.
		for _, p := range []struct{ pattern *regexp.Regexp }{{dailyDBRe}, {dailyImagesRe}} {
			removed, err := prune(*dest, p.pattern, *keep)
			if err != nil {
				log.Error("prune failed", slog.Any("err", err))
			}
			for _, f := range removed {
				log.Info("pruned old backup", slog.String("file", f))
			}
		}
		return ok
	}

	if *once {
		if !run() {
			return errors.New("backup failed; see the log above")
		}
		return nil
	}

	log.Info("backup loop started",
		slog.String("db", *dbPath),
		slog.String("dest", *dest),
		slog.String("interval", interval.String()),
		slog.Int("keep", *keep))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	retry := 5 * time.Minute
	if *interval < retry {
		retry = *interval
	}

	next := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			log.Info("backup loop stopping")
			return nil
		case <-time.After(next):
			if run() {
				next = *interval
			} else {
				next = retry
				log.Warn("retrying sooner after a failed backup", slog.String("in", next.String()))
			}
		}
	}
}

// backupDatabase writes a compacted, self-contained copy via VACUUM INTO,
// which is safe to run while the server is writing.
func backupDatabase(dbPath, out string) error {
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("database not found: %w", err)
	}
	// No migrations here: this process only reads. A second process running
	// migrations against a live database is exactly the kind of thing that
	// turns a backup into an outage.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(15000)")
	if err != nil {
		return err
	}
	defer db.Close()

	tmp := out + ".tmp"
	_ = os.Remove(tmp)
	if _, err := db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	// Rename last, so a partially written backup never appears under its final
	// name for a restore to pick up.
	return os.Rename(tmp, out)
}

// errNoImages means there is nothing to archive yet, which is normal on a new
// instance and must not be reported as a successful archive.
var errNoImages = errors.New("no image directory")

// archiveImages writes the content-addressed image tree to a gzipped tar.
func archiveImages(dir, out string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return errNoImages
	}

	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		os.Remove(tmp) // no-op once the rename below succeeds
	}()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	})
	if walkErr != nil {
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

// listBackups prints what is on the backup volume, newest first.
func listBackups(dir string, out io.Writer) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type row struct {
		name string
		size int64
		mod  time.Time
	}
	var rows []row
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		rows = append(rows, row{e.Name(), fi.Size(), fi.ModTime()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].mod.After(rows[j].mod) })
	if len(rows) == 0 {
		fmt.Fprintf(out, "no backups in %s\n", dir)
		return nil
	}
	for _, r := range rows {
		fmt.Fprintf(out, "%-40s %10.1f MiB  %s\n",
			r.name, float64(r.size)/(1<<20), r.mod.UTC().Format(time.RFC3339))
	}
	return nil
}

// dumpBackup streams one backup to stdout, so it can be retrieved with
// `kubectl exec ... > file` from a container that has no tar for kubectl cp.
//
// "latest" exists because the file names are UTC dates while the person typing
// the command is usually in some other timezone — `date +%F` names yesterday's
// file for several hours every evening.
func dumpBackup(dir, name string, out io.Writer) error {
	switch name {
	case "latest":
		resolved, err := newestMatching(dir, dailyDBRe)
		if err != nil {
			return err
		}
		name = resolved
	case "latest-images":
		resolved, err := newestMatching(dir, dailyImagesRe)
		if err != nil {
			return err
		}
		name = resolved
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("give a file name, not a path: %q", name)
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(out, f)
	return err
}

// Only the automatic daily files are pruned. A backup someone took by hand and
// named something else is theirs to delete.
var (
	dailyDBRe     = regexp.MustCompile(`^app-\d{4}-\d{2}-\d{2}\.db$`)
	dailyImagesRe = regexp.MustCompile(`^images-\d{4}-\d{2}-\d{2}\.tar\.gz$`)
)

// newestMatching returns the most recent file of a kind. The names carry ISO
// dates, so lexical order is chronological order.
func newestMatching(dir string, pattern *regexp.Regexp) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() && pattern.MatchString(e.Name()) && e.Name() > best {
			best = e.Name()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no backups of that kind in %s", dir)
	}
	return best, nil
}

func prune(dir string, pattern *regexp.Regexp, keep int) ([]string, error) {
	if keep < 1 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && pattern.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	// The names carry ISO dates, so lexical order is chronological order.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) <= keep {
		return nil, nil
	}

	var removed []string
	for _, name := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, err
		}
		removed = append(removed, name)
	}
	return removed, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	var n int
	if _, err := fmt.Sscanf(os.Getenv(key), "%d", &n); err == nil && n > 0 {
		return n
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		// Bare seconds, for compatibility with the old BACKUP_INTERVAL_SECONDS.
		var secs int
		if _, err := fmt.Sscanf(v, "%d", &secs); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return def
}

// verifyBackup opens a backup and says whether it is worth keeping.
//
// This exists because the restore how-to used to open with
// `sqlite3 app-....db "PRAGMA integrity_check;"`, and sqlite3 is exactly what is
// not available where it is needed: not in this scratch image, which the same
// document explains three paragraphs earlier, and not necessarily on the laptop
// somebody reaches for during an incident. The binary already links SQLite, so
// the check belongs here. A restore drill found this; nothing else would have.
//
// Read-only, and it never touches the live database unless you point it there.
func verifyBackup(dir, name string, out io.Writer) error {
	if name == "latest" {
		resolved, err := newestMatching(dir, dailyDBRe)
		if err != nil {
			return err
		}
		name = resolved
	}
	path := name
	if filepath.Base(name) == name {
		path = filepath.Join(dir, name)
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}

	// mode=ro so a corrupt file cannot be "repaired" into looking fine, and so
	// this is safe to run against a file something else is writing.
	sqldb, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer sqldb.Close()

	fmt.Fprintf(out, "%s\n", path)

	var integrity string
	if err := sqldb.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	fmt.Fprintf(out, "  %-24s %s\n", "integrity_check", integrity)

	counts := []struct{ label, query string }{
		{"users", `SELECT COUNT(*) FROM users`},
		{"lists", `SELECT COUNT(*) FROM lists`},
		{"items", `SELECT COUNT(*) FROM items WHERE deleted_at IS NULL`},
		{"items removed", `SELECT COUNT(*) FROM items WHERE deleted_at IS NOT NULL`},
		{"claims", `SELECT COUNT(*) FROM claims`},
		{"images", `SELECT COUNT(*) FROM item_images`},
		{"migrations applied", `SELECT COUNT(*) FROM schema_migrations`},
	}
	for _, c := range counts {
		var n int
		if err := sqldb.QueryRow(c.query).Scan(&n); err != nil {
			return fmt.Errorf("%s: %w", c.label, err)
		}
		fmt.Fprintf(out, "  %-24s %d\n", c.label, n)
	}

	// The §2.1 invariant. A backup that restores a database whose claimed counts
	// disagree with its claim rows restores the bug with it, and this is the
	// cheapest moment to notice.
	var drift int
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM items i
	     WHERE i.claimed_qty <> (SELECT COALESCE(SUM(c.qty), 0) FROM claims c WHERE c.item_id = i.id)`).
		Scan(&drift); err != nil {
		return fmt.Errorf("claim invariant: %w", err)
	}
	fmt.Fprintf(out, "  %-24s %d\n", "claim invariant drift", drift)

	if integrity != "ok" || drift != 0 {
		return errors.New("this backup has problems; do not restore it without looking")
	}
	return nil
}
