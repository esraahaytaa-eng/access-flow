package explorer

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
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
	r.Get("/audit/events/export", s.handleExport)
	r.Get("/audit/filters", s.handleFilters)
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

// eventFilter reads the event filters: q, action (comma-separated), actor, tool, from, to.
func (s *Service) eventFilter(r *http.Request) (EventFilter, error) {
	q := r.URL.Query()
	f := EventFilter{Text: strings.TrimSpace(q.Get("q"))}
	if a := q.Get("action"); a != "" {
		f.Actions = strings.Split(a, ",")
	}
	var err error
	if f.Actor, err = optionalID(r, "actor"); err != nil {
		return f, err
	}
	if f.Tool, err = optionalID(r, "tool"); err != nil {
		return f, err
	}
	if f.From, err = s.date(r, "from"); err != nil {
		return f, err
	}
	f.To, err = s.date(r, "to")
	return f, err
}

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	f, err := s.eventFilter(r)
	page, offset := 0, 0
	if err == nil {
		page, offset, err = platform.PageParam(r)
	}
	var events []Event
	total := 0
	if err == nil {
		events, total, err = s.Events(r.Context(), actor(r), f, offset)
	}
	platform.Respond(w, r, http.StatusOK, map[string]any{"events": events, "total": total, "page": page, "pageSize": platform.PageSize}, err)
}

// handleExport streams every event matching the filters as CSV. An error after the first
// byte aborts the response, so a client never takes a cut-off file for a complete one.
func (s *Service) handleExport(w http.ResponseWriter, r *http.Request) {
	f, err := s.eventFilter(r)
	var each func(func(Event) error) error
	if err == nil {
		each, err = s.EachEvent(r.Context(), actor(r), f)
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="accessflow_audit_`+time.Now().In(s.loc).Format(time.DateOnly)+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "\uFEFF") // lets spreadsheet tools read Turkish characters
	out := csv.NewWriter(w)
	_ = out.Write([]string{"occurred_at (" + s.loc.String() + ")", "actor_id", "actor", "action", "entity_type", "entity_id",
		"subject_id", "subject", "tool", "access_level", "account", "reason", "details"})
	err = each(func(e Event) error {
		details, err := json.Marshal(e.Details)
		if err != nil {
			return err
		}
		// Several items of one request: one value per item, "; " between them, in the same order.
		var tools, levels, accounts []string
		for _, a := range e.Access {
			tools, levels, accounts = append(tools, a.Tool), append(levels, deref(a.Level)), append(accounts, deref(a.Account))
		}
		join := func(v []string) string { return cell(strings.Join(v, "; ")) }
		return out.Write([]string{e.At.In(s.loc).Format(time.RFC3339Nano), personID(e.Actor), cell(personName(e.Actor, "System")), e.Action,
			e.Entity, idText(e.EntityID), personID(e.Subject), cell(personName(e.Subject, "")), join(tools), join(levels), join(accounts),
			cell(deref(e.Reason)), cell(string(details))})
	})
	if out.Flush(); err == nil {
		err = out.Error()
	}
	if err != nil {
		panic(http.ErrAbortHandler)
	}
}

// cell keeps text a user wrote (names, reasons, details) from starting a spreadsheet formula.
func cell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func personID(p *Person) string {
	if p == nil {
		return ""
	}
	return p.ID.String()
}

func personName(p *Person, none string) string {
	if p == nil {
		return none
	}
	return p.FullName
}

func idText(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Service) handleFilters(w http.ResponseWriter, r *http.Request) {
	actors, tools, err := s.Filters(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"actors": actors, "tools": tools}, err)
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
