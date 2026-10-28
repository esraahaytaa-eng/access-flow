// Command mocktools runs the local Mock Tool Service, standing in for the
// internal tools that AccessFlow provisions over HTTP (A1, A2). It has its own
// database and is reachable only on the internal Docker network.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(os.Args[1:]); err != nil {
		slog.Error("mocktools stopped", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	if len(args) > 0 && args[0] == "healthcheck" {
		return platform.Healthcheck(addr, "/healthz")
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", platform.HealthHandler(pool.Ping))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("listening", "addr", addr)
	return platform.Serve(ctx, srv)
}
