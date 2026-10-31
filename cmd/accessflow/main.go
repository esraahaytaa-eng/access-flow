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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/demo"
	"github.com/esraahaytaa-eng/access-flow/internal/explorer"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/onboarding"
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

	pool, err := platform.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	secret, err := auth.LoadSecret(ctx, pool, cfg.JWTSecret)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "reset-mail-key" { // explicit recovery of a lost mock email key (M18)
		n, err := auth.ResetMailKey(ctx, pool, cfg.MockEmailKeyFile)
		if err != nil {
			return err
		}
		fmt.Printf("%d open activation links revoked; restart the app (docker compose restart app), then issue new links\n", n)
		return nil
	}
	mailKey, err := auth.LoadMailKey(ctx, pool, cfg.MockEmailKeyFile)
	if errors.Is(err, auth.ErrMailKeyUnavailable) {
		// The app runs on; activation links can be neither issued nor shown until the reset.
		slog.Error("activation links unavailable", "err", err,
			"fix", "docker compose exec app /accessflow reset-mail-key, then docker compose restart app")
	} else if err != nil {
		return err
	}
	svc, err := auth.NewService(pool, auth.Options{Secret: secret, SecureCookies: cfg.SecureCookies,
		BootstrapFile: cfg.BootstrapFile, CompanyTimezone: cfg.CompanyTimezone, Logger: slog.Default(), MailKey: mailKey,
		LoginRateLimit: cfg.LoginRateLimit})
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "recover-admin" { // D2 last resort; read the password with bootstrap-password
		if len(args) != 2 {
			return errors.New("usage: accessflow recover-admin <email>")
		}
		return svc.RecoverAdmin(ctx, args[1])
	}
	// Demo mode (A4, D12 notes 2026-10-29): LoadConfig allowed it only in the local setup.
	var demoAdmin *auth.DemoAdmin
	var demoPasswords map[string]string
	var demoAccounts []demo.Account
	if cfg.Demo {
		if demoPasswords, err = demo.ParsePasswords(cfg.DemoPasswords); err != nil {
			return err
		}
		demoAdmin = &auth.DemoAdmin{FullName: demo.AdminName, Password: demoPasswords["admin"]}
		demoAccounts = demo.Accounts
		slog.Warn("demo mode on: documented demo accounts with known passwords; local use only")
	}
	if err := svc.Bootstrap(ctx, cfg.BootstrapAdminEmail, demoAdmin); err != nil {
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
	var client access.ToolClient = tools
	if cfg.GitHubToken != "" { // optional GitHub sandbox adapter; LoadConfig checked the settings
		if client, err = gitHubClient(ctx, pool, cfg, tools); err != nil {
			return err
		}
	}
	runner := access.NewRunner(pool, client)
	var wg sync.WaitGroup
	wg.Go(func() { runner.Run(ctx) })
	wg.Go(func() { sweeps.New(pool, loc, requests.TerminateDue).Run(ctx) })
	if cfg.Demo {
		// The seed needs the runner for its API operations, so it runs beside it; the
		// server starts at once and the demo data appears within seconds.
		services := demo.Services{Pool: pool, Auth: svc, People: people.NewService(pool, svc), Catalog: catalog.NewService(pool),
			Requests: requests.NewService(pool, loc), Access: access.NewService(pool, tools, loc), Reviews: reviews.NewService(pool),
			Tools: tools, Location: loc}
		wg.Go(func() {
			if err := demo.Seed(ctx, services, cfg.BootstrapAdminEmail, demoPasswords); err != nil && ctx.Err() == nil {
				slog.Error("demo seed failed", "err", err)
			}
		})
	}
	defer wg.Wait()
	defer stop() // runs first: a server that fails to start must stop the runner too

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newRouter(pool, svc, loc, tools, webui.Dist(), demoAccounts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("listening", "addr", cfg.HTTPAddr)
	return platform.Serve(ctx, srv)
}

// gitHubClient routes GITHUB_TOOL_ID to the GitHub sandbox adapter and every other tool to
// the Mock Tool Service. It fails closed: an unreachable or public repository, or a tool that
// is missing or not an API tool, stops the start. The token is never logged.
func gitHubClient(ctx context.Context, pool *pgxpool.Pool, cfg platform.Config, mock access.ToolClient) (access.ToolClient, error) {
	users, err := platform.ParseGitHubUsers(cfg.GitHubUsers)
	if err != nil {
		return nil, err
	}
	tool := uuid.MustParse(cfg.GitHubToolID) // checked by LoadConfig
	gh := access.NewGitHubToolClient(pool, "https://api.github.com", cfg.GitHubRepository, cfg.GitHubToken, users, 5*time.Second)
	if err := gh.CheckRepository(ctx); err != nil {
		return nil, err
	}
	if err := gh.CheckTool(ctx, tool); err != nil {
		return nil, err
	}
	slog.Info("GitHub sandbox adapter on", "repository", cfg.GitHubRepository, "tool", tool, "mappedUsers", len(users))
	return access.RouteTool(tool, gh, mock), nil
}

// newRouter serves the JSON API under /api and the web app on every other path. Demo
// accounts (demo mode only) are listed, without passwords, for the sign-in page.
func newRouter(pool *pgxpool.Pool, svc *auth.Service, loc *time.Location, tools *access.HTTPToolClient, dist fs.FS, demoAccounts []demo.Account) http.Handler {
	// Rejects cross-origin unsafe requests (Sec-Fetch-Site / Origin), on top of the CSRF token (A4).
	crossOrigin := http.NewCrossOriginProtection()
	crossOrigin.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		platform.WriteError(w, r, platform.NewError(http.StatusForbidden, "cross_origin_request"))
	}))

	peopleSvc := people.NewService(pool, svc)
	catalogSvc := catalog.NewService(pool)
	requestsSvc := requests.NewService(pool, loc)
	accessSvc := access.NewService(pool, tools, loc)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler { // no framing (clickjacking), no MIME sniffing, no referrer
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", "frame-ancestors 'none'")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			next.ServeHTTP(w, r)
		})
	})
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
			if demoAccounts != nil {
				r.Get("/demo", func(w http.ResponseWriter, r *http.Request) {
					platform.WriteJSON(w, http.StatusOK, map[string]any{"accounts": demoAccounts})
				})
			}
			r.Group(func(r chi.Router) {
				r.Use(svc.RequireSession)
				svc.RegisterActivations(r)
				peopleSvc.Register(r)
				catalogSvc.Register(r)
				requestsSvc.Register(r)
				accessSvc.Register(r)
				notify.Register(r, pool)
				reviews.NewService(pool).Register(r)
				explorer.NewService(pool, loc).Register(r)
				onboarding.NewService(pool, svc).Register(r)
			})
		})
	})
	if demoAccounts == nil { // R-7: the API documentation is for the local demo setup only
		r.Handle("/swagger", http.NotFoundHandler())
		r.Handle("/swagger/*", http.NotFoundHandler())
	}
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
