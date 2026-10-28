package access

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Service serves grants: removal, My Access and Team Access.
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Register adds the grant routes to r, which must already require a session.
func (s *Service) Register(r chi.Router) {
	r.Get("/access", s.handleMine)
	r.Get("/access/team", s.handleTeam)
	r.Post("/grants/{id}/revoke", s.handleRevoke)
	r.Post("/operations/{id}/retry", s.handleRetry)
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
