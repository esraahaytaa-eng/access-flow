package access

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Service serves grants: removal, My Access and Team Access, baseline imports and orphans.
type Service struct {
	pool  *pgxpool.Pool
	tools *HTTPToolClient // read by the baseline import (D10)
}

func NewService(pool *pgxpool.Pool, tools *HTTPToolClient) *Service {
	return &Service{pool: pool, tools: tools}
}

// Register adds the grant routes to r, which must already require a session.
func (s *Service) Register(r chi.Router) {
	r.Get("/access", s.handleMine)
	r.Get("/access/team", s.handleTeam)
	r.Get("/employees/{id}/grants", s.handleEmployeeGrants)
	r.Post("/grants/{id}/revoke", s.handleRevoke)
	r.Post("/operations/{id}/retry", s.handleRetry)
	r.Get("/fulfillment-tasks", s.handleTasks)
	r.Post("/fulfillment-tasks/{id}/claim", s.taskRoute("", func(ctx context.Context, a auth.Principal, id uuid.UUID, v int, _ string) (Task, error) {
		return s.Claim(ctx, a, id, v)
	}))
	r.Post("/fulfillment-tasks/{id}/ticket", s.taskRoute("ticketRef", s.RecordTicket))
	r.Post("/fulfillment-tasks/{id}/confirm", s.taskRoute("", func(ctx context.Context, a auth.Principal, id uuid.UUID, v int, _ string) (Task, error) {
		return s.Confirm(ctx, a, id, v)
	}))
	r.Post("/fulfillment-tasks/{id}/fail", s.taskRoute("reason", s.Fail))
	r.Post("/imports/mock", s.handleImportMock)
	r.Post("/imports/csv", s.handleImportCSV)
	r.Get("/imports", s.handleImports)
	r.Post("/orphans/{id}/revoke", s.orphanRoute(s.RevokeOrphan))
	r.Post("/orphans/{id}/dismiss", s.orphanRoute(s.DismissOrphan))
}

func actor(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func (s *Service) handleMine(w http.ResponseWriter, r *http.Request) {
	gs, err := s.Mine(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"grants": gs}, err)
}

func (s *Service) handleTeam(w http.ResponseWriter, r *http.Request) {
	gs, err := s.Team(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"grants": gs}, err)
}

func (s *Service) handleEmployeeGrants(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	var gs []Grant
	if err == nil {
		gs, err = s.EmployeeGrants(r.Context(), actor(r), ids[0])
	}
	platform.Respond(w, r, http.StatusOK, map[string]any{"grants": gs}, err)
}

func (s *Service) handleRevoke(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	var in struct {
		Version *int
		Reason  *string
	}
	if err == nil {
		err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
			return []platform.Field{{Name: "version", Present: in.Version != nil}, {Name: "reason", Present: in.Reason != nil}}
		})
	}
	var g Grant
	if err == nil {
		g, err = s.Revoke(r.Context(), actor(r), ids[0], *in.Version, *in.Reason)
	}
	platform.Respond(w, r, http.StatusOK, g, err)
}

func (s *Service) handleTasks(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Tasks(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"tasks": ts}, err)
}

// taskRoute serves a task action whose body is {version}, plus the text field when one is named.
func (s *Service) taskRoute(field string,
	do func(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, text string) (Task, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids, err := platform.PathIDs(r, "id")
		var in struct {
			Version   *int
			TicketRef *string
			Reason    *string
		}
		var text *string // the named field, once decoded
		if err == nil {
			err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
				text = in.TicketRef
				if field == "reason" {
					text = in.Reason
				}
				fs := []platform.Field{{Name: "version", Present: in.Version != nil}}
				if field != "" {
					fs = append(fs, platform.Field{Name: field, Present: text != nil})
				}
				return fs
			})
		}
		var t Task
		if err == nil {
			value := ""
			if text != nil {
				value = *text
			}
			t, err = do(r.Context(), actor(r), ids[0], *in.Version, value)
		}
		platform.Respond(w, r, http.StatusOK, t, err)
	}
}

func (s *Service) handleRetry(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	var in struct{ Version *int }
	if err == nil {
		err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
			return []platform.Field{{Name: "version", Present: in.Version != nil}}
		})
	}
	var op Operation
	if err == nil {
		op, err = s.Retry(r.Context(), actor(r), ids[0], *in.Version)
	}
	platform.Respond(w, r, http.StatusOK, op, err)
}

func (s *Service) handleImportMock(w http.ResponseWriter, r *http.Request) {
	var rep ImportReport
	err := platform.DecodeJSON(w, r, &struct{}{})
	if err == nil {
		rep, err = s.ImportMock(r.Context(), actor(r))
	}
	platform.Respond(w, r, http.StatusCreated, rep, err)
}

func (s *Service) handleImportCSV(w http.ResponseWriter, r *http.Request) {
	var in struct{ FileName, Content *string }
	err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "fileName", Present: in.FileName != nil}, {Name: "content", Present: in.Content != nil}}
	})
	var rep ImportReport
	if err == nil {
		rep, err = s.ImportCSV(r.Context(), actor(r), *in.FileName, *in.Content)
	}
	platform.Respond(w, r, http.StatusCreated, rep, err)
}

func (s *Service) handleImports(w http.ResponseWriter, r *http.Request) {
	list, err := s.Imports(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"imports": list}, err)
}

func (s *Service) orphanRoute(do func(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (Orphan, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids, err := platform.PathIDs(r, "id")
		var in struct {
			Version *int
			Reason  *string
		}
		if err == nil {
			err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
				return []platform.Field{{Name: "version", Present: in.Version != nil}, {Name: "reason", Present: in.Reason != nil}}
			})
		}
		var o Orphan
		if err == nil {
			o, err = do(r.Context(), actor(r), ids[0], *in.Version, *in.Reason)
		}
		platform.Respond(w, r, http.StatusOK, o, err)
	}
}
