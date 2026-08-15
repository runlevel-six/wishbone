package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"wishd/internal/auth"
)

// hashPasswordCmd prints an argon2id hash for a password read from stdin.
//
// It exists for exactly one situation: someone is locked out and there is no
// email, so no self-service reset (plan §0 rules out SMTP). An administrator
// pairs this with a one-line UPDATE and must_reset = 1, so the person is
// forced to choose their own password at next sign-in.
//
//	wishd hash-password
//	<type the temporary password, press enter>
//
// The password is read from stdin rather than taken as an argument so it does
// not land in shell history or the process table.
func hashPasswordCmd(stdin io.Reader, stdout io.Writer) error {
	fmt.Fprint(os.Stderr, "temporary password (10+ characters): ")

	reader := bufio.NewReader(stdin)
	line, err := reader.ReadString('\n')
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
	fmt.Fprintln(stdout, hash)
	fmt.Fprintln(os.Stderr, "\nApply it with, for example:")
	fmt.Fprintln(os.Stderr,
		`  sqlite3 /data/app.db "UPDATE users SET password_hash = '<hash>', must_reset = 1 WHERE username = '<user>';"`)
	fmt.Fprintln(os.Stderr,
		"\nmust_reset = 1 makes the app demand a new password at next sign-in.")
	return nil
}
