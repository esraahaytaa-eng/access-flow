package explorer

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func (s *Service) Register(r chi.Router) {
	r.Get("/audit/events", s.handleEvents)
	r.Get("/audit/access", s.handleAccess)
	r.Get("/audit/reports/privileged", s.handlePrivileged)
	r.Get("/audit/reports/overrides", s.eventReport(s.Overrides))
	r.Get("/audit/reports/policy", s.eventReport(s.PolicyChanges))
	r.Get("/audit/reports/leavers", s.handleLeavers)
	r.Get("/audit/reports/quarter", s.handleQuarter)
}

func actor(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

// date parses a query parameter as a date in the company timezone: its first instant.
func (s *Service) date(r *http.Request, name string) (*time.Time, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	d, err := time.ParseInLocation(time.DateOnly, v, s.loc)
	if err != nil {
		return nil, platform.ValidationError(name, "invalid_date")
	}
	return &d, nil
}

// optionalID parses a query parameter as an id.
func optionalID(r *http.Request, name string) (*uuid.UUID, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, platform.ValidationError(name, "invalid_id")
	}
	return &id, nil
}

// instant is the queried moment: "at" (RFC 3339), else the end of "date" in the company
// timezone, else now.
func (s *Service) instant(r *http.Request) (time.Time, error) {
	if v := r.URL.Query().Get("at"); v != "" {
		at, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return at, platform.ValidationError("at", "invalid_date")
		}
		return at, nil
	}
	d, err := s.date(r, "date")
	if err != nil {
		return time.Time{}, err
	}
	if d == nil {
		return time.Now(), nil
	}
	return d.AddDate(0, 0, 1).Add(-time.Microsecond), nil // PostgreSQL keeps microseconds
}

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := EventFilter{Text: strings.TrimSpace(q.Get("q"))}
	if a := q.Get("action"); a != "" {
		f.Actions = strings.Split(a, ",")
	}
	var err error
	f.Actor, err = optionalID(r, "actor")
	if err == nil {
		f.From, err = s.date(r, "from")
	}
	if err == nil {
		f.To, err = s.date(r, "to")
	}
	var events []Event
	truncated := false
	if err == nil {
		events, truncated, err = s.Events(r.Context(), actor(r), f)
	}
	platform.Respond(w, r, http.StatusOK, map[string]any{"events": events, "truncated": truncated}, err)
}

func (s *Service) handleAccess(w http.ResponseWriter, r *http.Request) {
	at, err := s.instant(r)
	var f AccessFilter
	if err == nil {
		f.Employee, err = optionalID(r, "employee")
	}
	if err == nil {
		f.Tool, err = optionalID(r, "tool")
	}
	var held, removed []Held
	if err == nil {
		held, removed, err = s.Access(r.Context(), actor(r), at, f)
	}
	platform.Respond(w, r, http.StatusOK, map[string]any{"at": at, "grants": held, "removed": removed}, err)
}

func (s *Service) handlePrivileged(w http.ResponseWriter, r *http.Request) {
	at, err := s.instant(r)
	var holders []Holder
	if err == nil {
		holders, err = s.Privileged(r.Context(), actor(r), at)
	}
	platform.Respond(w, r, http.StatusOK, map[string]any{"at": at, "holders": holders}, err)
}

// eventReport serves a report made of audit events.
func (s *Service) eventReport(report func(context.Context, auth.Principal) ([]Event, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := report(r.Context(), actor(r))
		platform.Respond(w, r, http.StatusOK, map[string]any{"rows": rows}, err)
	}
}

func (s *Service) handleLeavers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Leavers(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"rows": rows}, err)
}

func (s *Service) handleQuarter(w http.ResponseWriter, r *http.Request) {
	q, err := s.LatestQuarter(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, q, err)
}
