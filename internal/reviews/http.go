package reviews

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func (s *Service) Register(r chi.Router) {
	r.Get("/reviews", s.handlePage)
	r.Post("/reviews/campaigns", s.handleStart)
	r.Post("/review-items/{id}/decide", s.handleDecide)
	r.Post("/review-items/bulk-keep", s.handleBulkKeep)
}

func actor(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func (s *Service) handlePage(w http.ResponseWriter, r *http.Request) {
	page, err := s.Page(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, page, err)
}

func (s *Service) handleStart(w http.ResponseWriter, r *http.Request) {
	var c Campaign
	err := platform.DecodeJSON(w, r, &struct{}{})
	if err == nil {
		c, err = s.StartCampaign(r.Context(), actor(r))
	}
	platform.Respond(w, r, http.StatusCreated, c, err)
}

func (s *Service) handleDecide(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Decision *string
		Reason   *string
		Version  *int
	}
	ids, err := platform.PathIDs(r, "id")
	if err == nil {
		err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
			return []platform.Field{{Name: "decision", Present: in.Decision != nil}, {Name: "version", Present: in.Version != nil}}
		})
	}
	var it Item
	if err == nil {
		reason := ""
		if in.Reason != nil {
			reason = *in.Reason
		}
		it, err = s.Decide(r.Context(), actor(r), ids[0], *in.Decision, reason, *in.Version)
	}
	platform.Respond(w, r, http.StatusOK, it, err)
}

func (s *Service) handleBulkKeep(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items *[]struct {
			ID      uuid.UUID
			Version int
		}
	}
	err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "items", Present: in.Items != nil}}
	})
	kept, skipped := 0, 0
	if err == nil {
		var items []Versioned
		for _, it := range *in.Items {
			items = append(items, Versioned{it.ID, it.Version})
		}
		kept, skipped, err = s.BulkKeep(r.Context(), actor(r), items)
	}
	platform.Respond(w, r, http.StatusOK, map[string]int{"kept": kept, "skipped": skipped}, err)
}
