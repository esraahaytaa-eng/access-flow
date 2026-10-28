// Command accessflow runs the AccessFlow server: API, background work and the web UI.
package main

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/webui"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(os.Args[1:]); err != nil {
		slog.Error("accessflow stopped", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := platform.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "healthcheck" {
		return platform.Healthcheck(cfg.HTTPAddr, "/api/v1/health")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := platform.Migrate(ctx, cfg.MigrationDatabaseURL); err != nil {
		return err
	}
	slog.Info("migrations applied")

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newRouter(pool, webui.Dist()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("listening", "addr", cfg.HTTPAddr)
	return platform.Serve(ctx, srv)
}

// newRouter serves the JSON API under /api and the web app on every other path.
func newRouter(pool *pgxpool.Pool, dist fs.FS) http.Handler {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Method(http.MethodGet, "/v1/health", platform.HealthHandler(pool.Ping))
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			platform.WriteError(w, r, platform.ErrNotFound)
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			platform.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		})
	})
	r.Handle("/*", webui.Handler(dist))
	return r
}
