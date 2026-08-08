// Command kaku runs the Kaku CMS server.
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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/RizkyChandra/kaku/internal/admin"
	"github.com/RizkyChandra/kaku/internal/api"
	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/backup"
	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/content"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/i18n"
	"github.com/RizkyChandra/kaku/internal/media"
	"github.com/RizkyChandra/kaku/internal/web/static"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

// version is stamped by goreleaser via -ldflags.
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsDev() {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	} else {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	slog.Info("database ready", "path", cfg.DBPath)
	q := db.New(sqlDB)
	view.AssetVersion = version

	if err := i18n.Load(cfg.LocalesDir); err != nil {
		return err
	}
	slog.Info("locales loaded", "count", len(i18n.Available()), "dir", cfg.LocalesDir)

	if err := auth.EnsureRoot(ctx, q, cfg); err != nil {
		return err
	}
	if err := q.DeleteExpiredSessions(ctx); err != nil {
		return fmt.Errorf("prune sessions: %w", err)
	}
	// Expired sessions are already rejected at lookup, so this is hygiene: a
	// long-lived process would otherwise accumulate dead rows forever.
	go prune(ctx, q)
	sessions := auth.New(q, !cfg.IsDev())

	// Media is optional: Kaku runs fine with no object storage, uploads just fail.
	var store *media.Store
	if cfg.S3.Bucket != "" {
		store, err = media.New(ctx, cfg.S3)
		if err != nil {
			return err
		}
		if err := store.EnsureBucket(ctx); err != nil {
			slog.Warn("could not ensure media bucket exists", "bucket", cfg.S3.Bucket, "err", err)
		}
	} else {
		slog.Warn("no KAKU_S3_BUCKET configured; media uploads are disabled")
	}

	scheduler := &content.Scheduler{PublishDue: func(ctx context.Context) (int64, error) {
		return q.PublishDuePosts(ctx)
	}}
	go scheduler.Run(ctx)

	// Backups need object storage; without a bucket this is a no-op rather than
	// a startup failure, matching how media degrades.
	backups, err := backup.New(ctx, cfg)
	if err != nil && !errors.Is(err, backup.ErrNoBucket) {
		return err
	}
	if backups != nil {
		go backups.Run(ctx)
	} else {
		slog.Warn("no object storage configured; database backups are disabled")
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"` + version + `"}`))
	})
	r.Handle("/static/*", http.StripPrefix("/static/", staticHandler(cfg)))
	r.Mount("/admin", admin.New(q, sessions, store, backups, cfg).Router())
	r.Mount("/api/v1", api.New(q).Router())
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusFound)
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("kaku listening", "addr", cfg.Addr, "version", version, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// prune clears expired session rows on a slow ticker. Lookups already reject
// them, so this only keeps the table from growing without bound.
func prune(ctx context.Context, q *db.Queries) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := q.DeleteExpiredSessions(ctx); err != nil {
				slog.ErrorContext(ctx, "prune sessions", "err", err)
			}
		}
	}
}

func staticHandler(cfg config.Config) http.Handler {
	h := http.FileServer(http.FS(static.FS))
	if cfg.IsDev() {
		return h
	}
	// Assets are embedded and only change with the binary, so a long cache is safe.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		h.ServeHTTP(w, r)
	})
}
