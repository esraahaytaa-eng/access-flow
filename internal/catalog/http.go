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

// pathIDs reads UUID path parameters; a malformed one is simply not found.
func pathIDs(r *http.Request, names ...string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, len(names))
	for i, name := range names {
		id, err := uuid.Parse(chi.URLParam(r, name))
		if err != nil {
			return nil, platform.ErrNotFound
		}
		ids[i] = id
	}
	return ids, nil
}

type field struct {
	name    string
	present bool
}

// decode reads the body into in, then requires every listed field: a forgotten key never
// silently drops an approval step or changes a value.
func decode(w http.ResponseWriter, r *http.Request, in any, fields func() []field) error {
	if err := platform.DecodeJSON(w, r, in); err != nil {
		return err
	}
	for _, f := range fields() {
		if !f.present {
			return platform.ValidationError(f.name, "field_required")
		}
	}
	return nil
}

func respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, status, v)
}

func (s *Service) handleCreateTool(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, FulfillmentMethod *string
		OwnerID, AdminGroupID   *uuid.UUID
	}
	if err := decode(w, r, &in, func() []field {
		return []field{{"name", in.Name != nil}, {"fulfillmentMethod", in.FulfillmentMethod != nil},
			{"ownerId", in.OwnerID != nil}, {"adminGroupId", in.AdminGroupID != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	t, err := s.CreateTool(r.Context(), actor(r), NewTool{*in.Name, *in.FulfillmentMethod, *in.OwnerID, *in.AdminGroupID})
	respond(w, r, http.StatusCreated, t, err)
}

func (s *Service) handleUpdateTool(w http.ResponseWriter, r *http.Request) {
	ids, err := pathIDs(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in struct {
		Name                  *string
		OwnerID, AdminGroupID *uuid.UUID
		Version               *int
	}
	if err := decode(w, r, &in, func() []field {
		return []field{{"name", in.Name != nil}, {"ownerId", in.OwnerID != nil},
			{"adminGroupId", in.AdminGroupID != nil}, {"version", in.Version != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	t, err := s.UpdateTool(r.Context(), actor(r), ids[0], ToolUpdate{*in.Name, *in.OwnerID, *in.AdminGroupID, *in.Version})
	respond(w, r, http.StatusOK, t, err)
}

// decodeVersion reads a {"version": n} body.
func decodeVersion(w http.ResponseWriter, r *http.Request) (int, error) {
	var in struct{ Version *int }
	err := decode(w, r, &in, func() []field { return []field{{"version", in.Version != nil}} })
	if err != nil {
		return 0, err
	}
	return *in.Version, nil
}

func (s *Service) handleRetireTool(w http.ResponseWriter, r *http.Request) {
	ids, err := pathIDs(r, "id")
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
	respond(w, r, http.StatusOK, t, err)
}

func (s *Service) handleCreateLevel(w http.ResponseWriter, r *http.Request) {
	ids, err := pathIDs(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in struct {
		Name                               *string
		RequiresToolOwner, RequiresInfosec *bool
	}
	if err := decode(w, r, &in, func() []field {
		return []field{{"name", in.Name != nil}, {"requiresToolOwner", in.RequiresToolOwner != nil},
			{"requiresInfosec", in.RequiresInfosec != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	l, err := s.CreateLevel(r.Context(), actor(r), ids[0], NewLevel{*in.Name, *in.RequiresToolOwner, *in.RequiresInfosec})
	respond(w, r, http.StatusCreated, l, err)
}

func (s *Service) handleUpdateLevel(w http.ResponseWriter, r *http.Request) {
	ids, err := pathIDs(r, "id", "levelId")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in struct {
		Name                               *string
		RequiresToolOwner, RequiresInfosec *bool
		Version                            *int
	}
	if err := decode(w, r, &in, func() []field {
		return []field{{"name", in.Name != nil}, {"requiresToolOwner", in.RequiresToolOwner != nil},
			{"requiresInfosec", in.RequiresInfosec != nil}, {"version", in.Version != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	l, err := s.UpdateLevel(r.Context(), actor(r), ids[0], ids[1], LevelUpdate{*in.Name, *in.RequiresToolOwner, *in.RequiresInfosec, *in.Version})
	respond(w, r, http.StatusOK, l, err)
}

func (s *Service) handleRetireLevel(w http.ResponseWriter, r *http.Request) {
	ids, err := pathIDs(r, "id", "levelId")
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
	respond(w, r, http.StatusOK, l, err)
}
