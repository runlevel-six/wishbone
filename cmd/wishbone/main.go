// Command wishbone serves Wishbone, a self-hosted family wishlist.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wishbone/internal/auth"
	"wishbone/internal/config"
	"wishbone/internal/db"
	"wishbone/internal/extract"
	"wishbone/internal/fetch"
	"wishbone/internal/imgstore"
	"wishbone/internal/model"
	"wishbone/internal/store"
	"wishbone/internal/web"
)

// version is stamped at build time (see the Makefile and Dockerfile). It is
// logged at startup and printed by `wishbone version` so that "which build is
// actually running?" is never a guess — an unanswered version question has
// already cost more debugging time here than the bugs did.
var version = "dev"

const usage = `usage: wishbone [command]

With no command, wishbone serves Wishbone. Configuration comes from the
environment; see docs/reference/configuration.md.

Commands:
  backup          Periodically copy the database and images to a backup volume.
                  Flags: -dest -interval -keep -once -db -images
  check-url       Run one URL through the real extraction pipeline and report
                  each phase: DNS, connect, TLS, and what was extracted.
  set-password    Set a temporary password for one account and force a change
                  at next sign-in. Flags: -user -db -no-force-reset
  hash-password   Print an argon2id hash for a password read from stdin.
  version         Print the build version.
  help            This text.
`

func main() {
	// A few subcommands share the binary. They exist because the image is a
	// static binary on scratch: there is no shell in the container to run
	// sqlite3 or a backup script from, so anything an operator needs to do has
	// to be something the binary itself can do.
	if len(os.Args) > 1 {
		fail := func(err error) {
			if err != nil {
				fmt.Fprintln(os.Stderr, "wishbone:", err)
				os.Exit(1)
			}
		}
		switch os.Args[1] {
		case "backup":
			fail(backupCmd(os.Args[2:]))
			return
		case "check-url":
			fail(checkURLCmd(os.Args[2:], os.Stdout))
			return
		case "set-password":
			fail(setPasswordCmd(os.Args[2:], os.Stdin, os.Stdout))
			return
		case "hash-password":
			fail(hashPasswordCmd(os.Stdin, os.Stdout))
			return
		case "version", "--version":
			fmt.Println(version)
			return
		case "-h", "--help", "help":
			fmt.Fprint(os.Stderr, usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "wishbone: unknown command %q\n\n%s", os.Args[1], usage)
			os.Exit(2)
		}
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "wishbone:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqldb, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer sqldb.Close()
	st := store.New(sqldb)

	client := fetch.New(fetch.Options{
		UserAgent:      cfg.FetchUserAgent,
		AcceptLanguage: cfg.FetchLang,
		Impersonate:    cfg.FetchImpersonate,
	})
	sidecar := extract.NewSidecar(cfg.SidecarURL, cfg.SidecarTimeout)
	extractor := extract.NewService(client, sidecar, cfg.FetchEnabled)

	images, err := imgstore.New(cfg.ImageDir, client)
	if err != nil {
		return err
	}

	if err := bootstrapAdmin(ctx, st, cfg, log); err != nil {
		return err
	}

	srv := web.NewServer(cfg, st, extractor, images, log)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go janitor(ctx, st, srv, log)

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening",
			slog.String("version", version),
			slog.String("addr", cfg.Addr),
			slog.String("db", cfg.DBPath),
			slog.Bool("fetch", cfg.FetchEnabled),
			slog.Bool("sidecar", cfg.SidecarURL != ""))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	if cfg.SecretIsEphemeral {
		log.Warn("WISHBONE_SECRET_KEY is unset; a random key was generated for this process")
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}

// bootstrapAdmin creates the first account when the database is empty, so a
// fresh deployment is reachable without a shell. Registration is invite-only
// after that (plan §1).
func bootstrapAdmin(ctx context.Context, st *store.Store, cfg *config.Config, log *slog.Logger) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if cfg.BootstrapAdmin == "" || cfg.BootstrapAdminPassword == "" {
		log.Warn("no users yet: set WISHBONE_BOOTSTRAP_ADMIN and WISHBONE_BOOTSTRAP_ADMIN_PASSWORD to create the first account")
		return nil
	}
	if len(cfg.BootstrapAdminPassword) < 10 {
		return errors.New("WISHBONE_BOOTSTRAP_ADMIN_PASSWORD must be at least 10 characters")
	}
	hash, err := auth.HashPassword(cfg.BootstrapAdminPassword)
	if err != nil {
		return err
	}
	u := &model.User{
		Username:     cfg.BootstrapAdmin,
		DisplayName:  cfg.BootstrapAdmin,
		PasswordHash: hash,
		IsAdmin:      true,
	}
	if err := st.CreateUser(ctx, u); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	log.Info("created bootstrap admin", slog.String("username", u.Username))
	return nil
}

// janitor does the small periodic housekeeping: expired sessions and the login
// rate-limiter buckets.
func janitor(ctx context.Context, st *store.Store, srv *web.Server, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := st.DeleteExpiredSessions(ctx, model.TimeString(model.Now())); err != nil {
				log.Warn("session sweep", slog.Any("err", err))
			} else if n > 0 {
				log.Info("expired sessions removed", slog.Int64("count", n))
			}
			srv.SweepLimiters()
		}
	}
}
