package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Error is a typed business error. WriteError turns it into an HTTP response with a
// stable machine-readable code; the UI translates codes (A7: English and Turkish).
type Error struct {
	Status int
	Code   string
	Field  string // set for validation errors
}

func (e *Error) Error() string {
	if e.Field != "" {
		return e.Code + " (" + e.Field + ")"
	}
	return e.Code
}

// NewError returns a typed error with an HTTP status and a code.
func NewError(status int, code string) *Error { return &Error{Status: status, Code: code} }

// ValidationError reports invalid input in field.
func ValidationError(field, code string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Field: field}
}

var (
	ErrNotFound     = NewError(http.StatusNotFound, "not_found")
	ErrUnauthorized = NewError(http.StatusUnauthorized, "authentication_required")
	ErrForbidden    = NewError(http.StatusForbidden, "forbidden")
)

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response", "err", err)
	}
}

// WriteError maps typed errors to HTTP responses in one place (A8).
// Unknown errors are logged and answered with a generic 500, never their text.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if errors.As(err, &e) {
		body := map[string]string{"error": e.Code}
		if e.Field != "" {
			body["field"] = e.Field
		}
		WriteJSON(w, e.Status, body)
		return
	}
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
}

// maxBodyBytes caps JSON request bodies; nothing in the API needs more.
const maxBodyBytes = 64 << 10

// DecodeJSON reads a JSON request body into v, rejecting unknown fields and bodies
// over 64 KiB with an invalid_json validation error.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return ValidationError("", "invalid_json")
	}
	return nil
}

// HealthHandler reports 200 while ping succeeds and 503 otherwise.
func HealthHandler(ping func(context.Context) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ping(ctx); err != nil {
			slog.Warn("health check: database unavailable", "err", err)
			WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database_unavailable"})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

// Healthcheck is the container health probe: it calls path on the local server
// listening on addr (":8080" or "host:8080") and succeeds only on a 200.
// The runtime images have no curl, so each binary runs this as a subcommand.
func Healthcheck(addr, path string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("health check address %q: %w", addr, err)
	}
	url := "http://" + net.JoinHostPort("127.0.0.1", port) + path
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check %s: status %d", url, resp.StatusCode)
	}
	return nil
}

// Serve runs srv until ctx is cancelled, then shuts it down gracefully.
func Serve(ctx context.Context, srv *http.Server) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
