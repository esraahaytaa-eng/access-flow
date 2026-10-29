// Command accessflow runs the AccessFlow server: API, background work and the web UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/devfixture"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/requests"
	"github.com/esraahaytaa-eng/access-flow/internal/reviews"
	"github.com/esraahaytaa-eng/access-flow/internal/sweeps"
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
	if len(args) > 0 {
		switch args[0] {
		case "healthcheck":
			return platform.Healthcheck(cfg.HTTPAddr, "/api/v1/health")
		case "bootstrap-password":
			return printBootstrapPassword(cfg.BootstrapFile, os.Stdout)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := platform.Migrate(ctx, cfg.MigrationDatabaseURL, platform.Migrations); err != nil {
		return err
	}
	slog.Info("migrations applied")

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	secret, err := auth.LoadSecret(ctx, pool, cfg.JWTSecret)
	if err != nil {
		return err
	}
	svc, err := auth.NewService(pool, auth.Options{Secret: secret, SecureCookies: cfg.SecureCookies,
		BootstrapFile: cfg.BootstrapFile, CompanyTimezone: cfg.CompanyTimezone, Logger: slog.Default()})
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "dev-fixture" {
		return runDevFixture(ctx, people.NewService(pool, svc), catalog.NewService(pool), cfg.SeedPassword, os.Stdout)
	}
	if err := svc.Bootstrap(ctx, cfg.BootstrapAdminEmail); err != nil {
		return err
	}
	if err := svc.RemoveFinishedBootstrapFile(ctx); err != nil {
		return err
	}

	loc, err := time.LoadLocation(cfg.CompanyTimezone) // checked by LoadConfig
	if err != nil {
		return err
	}
	// One runner and one sweeps loop (A2: one instance); they stop with ctx and finish
	// before the pool closes.
	tools := access.NewHTTPToolClient(cfg.MockToolsURL, 5*time.Second)
	runner := access.NewRunner(pool, tools)
	var wg sync.WaitGroup
	wg.Go(func() { runner.Run(ctx) })
	wg.Go(func() { sweeps.New(pool, loc, requests.TerminateDue).Run(ctx) })
	defer wg.Wait()
	defer stop() // runs first: a server that fails to start must stop the runner too

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newRouter(pool, svc, loc, tools, webui.Dist()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("listening", "addr", cfg.HTTPAddr)
	return platform.Serve(ctx, srv)
}

// newRouter serves the JSON API under /api and the web app on every other path.
func newRouter(pool *pgxpool.Pool, svc *auth.Service, loc *time.Location, tools *access.HTTPToolClient, dist fs.FS) http.Handler {
	// Rejects cross-origin unsafe requests (Sec-Fetch-Site / Origin), on top of the CSRF token (A4).
	crossOrigin := http.NewCrossOriginProtection()
	crossOrigin.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		platform.WriteError(w, r, platform.NewError(http.StatusForbidden, "cross_origin_request"))
	}))

	peopleSvc := people.NewService(pool, svc)
	catalogSvc := catalog.NewService(pool)
	requestsSvc := requests.NewService(pool, loc)
	accessSvc := access.NewService(pool, tools)

	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(crossOrigin.Handler)
		// Set before the routes below, so the /v1 subrouter inherits the JSON answers.
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			platform.WriteError(w, r, platform.ErrNotFound)
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			platform.WriteError(w, r, platform.NewError(http.StatusMethodNotAllowed, "method_not_allowed"))
		})
		r.Route("/v1", func(r chi.Router) {
			r.Method(http.MethodGet, "/health", platform.HealthHandler(pool.Ping))
			r.Mount("/auth", svc.Routes())
			r.Group(func(r chi.Router) {
				r.Use(svc.RequireSession)
				peopleSvc.Register(r)
				catalogSvc.Register(r)
				requestsSvc.Register(r)
				accessSvc.Register(r)
				notify.Register(r, pool)
				reviews.NewService(pool).Register(r)
			})
		})
	})
	r.Handle("/*", webui.Handler(dist))
	return r
}

// printBootstrapPassword shows the initial Super Admin's one-time password file
// (the distroless image has no cat).
func printBootstrapPassword(file string, out io.Writer) error {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no bootstrap password at %s: it was already changed, or this database had a Super Admin before", file)
	}
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

// runDevFixture creates the development employees, groups and tools on request (never at startup).
func runDevFixture(ctx context.Context, svc *people.Service, cat *catalog.Service, password string, out io.Writer) error {
	created, err := devfixture.Run(ctx, svc, cat, password)
	for _, email := range created {
		fmt.Fprintln(out, "created", email)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%d records created; fixture employees sign in with the SEED_PASSWORD value (local only)\n", len(created))
	return nil
}
