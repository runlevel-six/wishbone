// Command wishd serves Wishbone, a self-hosted family wishlist.
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

	"wishd/internal/auth"
	"wishd/internal/config"
	"wishd/internal/db"
	"wishd/internal/extract"
	"wishd/internal/fetch"
	"wishd/internal/imgstore"
	"wishd/internal/model"
	"wishd/internal/store"
	"wishd/internal/web"
)

func main() {
	// One subcommand, for the locked-out-family-member case. Everything else
	// is the server.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hash-password":
			if err := hashPasswordCmd(os.Stdin, os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "wishd:", err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			fmt.Fprintln(os.Stderr, "usage: wishd [hash-password]\n\n"+
				"With no arguments, wishd serves Wishbone. Configuration is read from\n"+
				"the environment; see docs/reference/configuration.md.")
			return
		default:
			fmt.Fprintf(os.Stderr, "wishd: unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "wishd:", err)
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
			slog.String("addr", cfg.Addr),
			slog.String("db", cfg.DBPath),
			slog.Bool("fetch", cfg.FetchEnabled),
			slog.Bool("sidecar", cfg.SidecarURL != ""))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	if cfg.SecretIsEphemeral {
		log.Warn("WISHD_SECRET_KEY is unset; a random key was generated for this process")
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
		log.Warn("no users yet: set WISHD_BOOTSTRAP_ADMIN and WISHD_BOOTSTRAP_ADMIN_PASSWORD to create the first account")
		return nil
	}
	if len(cfg.BootstrapAdminPassword) < 10 {
		return errors.New("WISHD_BOOTSTRAP_ADMIN_PASSWORD must be at least 10 characters")
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
