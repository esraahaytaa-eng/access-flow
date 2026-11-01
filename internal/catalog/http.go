package catalog

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Register adds the catalog routes to r, which must already require a session.
func (s *Service) Register(r chi.Router) {
	r.Get("/tools", s.handleCatalog)
	r.Post("/tools", s.handleCreateTool)
	r.Put("/tools/{id}", s.handleUpdateTool)
	r.Post("/tools/{id}/retire", s.handleRetireTool)
	r.Post("/tools/{id}/levels", s.handleCreateLevel)
	r.Put("/tools/{id}/levels/{levelId}", s.handleUpdateLevel)
	r.Post("/tools/{id}/levels/{levelId}/retire", s.handleRetireLevel)
	r.Get("/starter-packages", s.handleListPackages)
	r.Post("/starter-packages", s.handleAddPackage)
	r.Delete("/starter-packages/{id}", s.handleRemovePackage)
}

func actor(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func (s *Service) handleCreateTool(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, FulfillmentMethod *string
		OwnerID, AdminGroupID   *uuid.UUID
		AccountKind             string // optional
	}
	if err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "name", Present: in.Name != nil}, {Name: "fulfillmentMethod", Present: in.FulfillmentMethod != nil},
			{Name: "ownerId", Present: in.OwnerID != nil}, {Name: "adminGroupId", Present: in.AdminGroupID != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	t, err := s.CreateTool(r.Context(), actor(r), NewTool{*in.Name, *in.FulfillmentMethod, *in.OwnerID, *in.AdminGroupID, in.AccountKind})
	platform.Respond(w, r, http.StatusCreated, t, err)
}

func (s *Service) handleUpdateTool(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in struct {
		Name                  *string
		OwnerID, AdminGroupID *uuid.UUID
		Version               *int
	}
	if err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "name", Present: in.Name != nil}, {Name: "ownerId", Present: in.OwnerID != nil},
			{Name: "adminGroupId", Present: in.AdminGroupID != nil}, {Name: "version", Present: in.Version != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	t, err := s.UpdateTool(r.Context(), actor(r), ids[0], ToolUpdate{*in.Name, *in.OwnerID, *in.AdminGroupID, *in.Version})
	platform.Respond(w, r, http.StatusOK, t, err)
}

// decodeVersion reads a {"version": n} body.
func decodeVersion(w http.ResponseWriter, r *http.Request) (int, error) {
	var in struct{ Version *int }
	err := platform.DecodeRequired(w, r, &in, func() []platform.Field { return []platform.Field{{Name: "version", Present: in.Version != nil}} })
	if err != nil {
		return 0, err
	}
	return *in.Version, nil
}

func (s *Service) handleRetireTool(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	version, err := decodeVersion(w, r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	t, err := s.RetireTool(r.Context(), actor(r), ids[0], version)
	platform.Respond(w, r, http.StatusOK, t, err)
}

func (s *Service) handleCreateLevel(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in struct {
		Name                               *string
		RequiresToolOwner, RequiresInfosec *bool
	}
	if err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "name", Present: in.Name != nil}, {Name: "requiresToolOwner", Present: in.RequiresToolOwner != nil},
			{Name: "requiresInfosec", Present: in.RequiresInfosec != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	l, err := s.CreateLevel(r.Context(), actor(r), ids[0], NewLevel{*in.Name, *in.RequiresToolOwner, *in.RequiresInfosec})
	platform.Respond(w, r, http.StatusCreated, l, err)
}

func (s *Service) handleUpdateLevel(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id", "levelId")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in struct {
		Name                               *string
		RequiresToolOwner, RequiresInfosec *bool
		Version                            *int
	}
	if err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "name", Present: in.Name != nil}, {Name: "requiresToolOwner", Present: in.RequiresToolOwner != nil},
			{Name: "requiresInfosec", Present: in.RequiresInfosec != nil}, {Name: "version", Present: in.Version != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	l, err := s.UpdateLevel(r.Context(), actor(r), ids[0], ids[1], LevelUpdate{*in.Name, *in.RequiresToolOwner, *in.RequiresInfosec, *in.Version})
	platform.Respond(w, r, http.StatusOK, l, err)
}

func (s *Service) handleRetireLevel(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id", "levelId")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	version, err := decodeVersion(w, r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	l, err := s.RetireLevel(r.Context(), actor(r), ids[0], ids[1], version)
	platform.Respond(w, r, http.StatusOK, l, err)
}
