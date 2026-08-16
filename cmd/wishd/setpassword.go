package main

import (
	"bufio"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"wishd/internal/auth"
)

// setPasswordCmd sets a temporary password for one account and forces a change
// at next sign-in.
//
// This is the recovery path for someone locked out. There is no email, so no
// self-service reset, and the image has no shell to run sqlite3 from — so the
// binary does the whole job:
//
//	wishd set-password -user sam
//
// It writes to the live database, which is safe: SQLite is in WAL mode and the
// server tolerates a second writer. must_reset is set so the temporary
// password is good for exactly one sign-in.
func setPasswordCmd(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("set-password", flag.ContinueOnError)
	var (
		username = fs.String("user", "", "username to reset (required)")
		dbPath   = fs.String("db", env("WISHD_DB_PATH", filepath.Join(env("WISHD_DATA_DIR", "/data"), "app.db")), "database file")
		noReset  = fs.Bool("no-force-reset", false, "do not require a password change at next sign-in")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		fs.Usage()
		return errors.New("-user is required")
	}

	fmt.Fprint(os.Stderr, "temporary password (10+ characters): ")
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	password := strings.TrimRight(line, "\r\n")
	if len(password) < 10 {
		return errors.New("password must be at least 10 characters")
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	db, err := sql.Open("sqlite", "file:"+*dbPath+"?_pragma=busy_timeout(15000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return err
	}
	defer db.Close()

	mustReset := 1
	if *noReset {
		mustReset = 0
	}
	res, err := db.Exec(
		`UPDATE users SET password_hash = ?, must_reset = ? WHERE username = ? COLLATE NOCASE`,
		hash, mustReset, *username)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no user named %q", *username)
	}

	// Existing sessions keep working unless they are cleared, which would be
	// surprising for an admin merely helping someone back in. Clear them
	// explicitly when the account is being recovered, since a forgotten
	// password and a compromised one look the same from here.
	if _, err := db.Exec(
		`DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE username = ? COLLATE NOCASE)`,
		*username); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "password set for %s; other sessions signed out\n", *username)
	if mustReset == 1 {
		fmt.Fprintln(stdout, "they will be required to choose a new password at next sign-in")
	}
	return nil
}
