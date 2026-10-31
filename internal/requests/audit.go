package requests

import (
	"context"
	"net/http"
	"strings"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/explorer"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Audited is the Audit Explorer's approval evidence (CASE §5): requests in the actor's
// audit scope with each item's approval chain, newest first, optionally matching text.
func (s *Service) Audited(ctx context.Context, actor auth.Principal, text string) ([]Request, error) {
	scope, err := explorer.ReportScope(ctx, s.pool, actor)
	if err != nil {
		return nil, err
	}
	var w explorer.Where
	scope.Restrict(&w, "r.beneficiary_id")
	if text != "" {
		p := explorer.Like(text)
		w.Add(`(r.id::text ILIKE ? OR b.full_name ILIKE ? OR EXISTS (SELECT 1 FROM request_items i
			JOIN tools t ON t.id = i.tool_id JOIN access_levels l ON l.id = i.access_level_id
			WHERE i.request_id = r.id AND (t.name ILIKE ? OR l.name ILIKE ?)))`, p, p, p, p)
	}
	return loadRequests(ctx, s.pool, w.SQL(), w.Args...)
}

func (s *Service) handleAudited(w http.ResponseWriter, r *http.Request) {
	reqs, err := s.Audited(r.Context(), actor(r), strings.TrimSpace(r.URL.Query().Get("q")))
	platform.Respond(w, r, http.StatusOK, map[string]any{"requests": reqs}, err)
}
