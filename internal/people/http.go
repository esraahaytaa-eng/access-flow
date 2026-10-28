package people

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Register adds the people routes to r, which must already require a session.
func (s *Service) Register(r chi.Router) {
	r.Get("/employees", s.handleList)
	r.Post("/employees", s.handleCreate)
	r.Put("/employees/{id}", s.handleUpdate)
	r.Post("/employees/{id}/password-reset", s.handleReset)
	r.Post("/employees/{id}/roles", s.handleAssignRole)
	r.Delete("/employees/{id}/roles/{role}", s.handleRemoveRole)
	r.Get("/admin-groups", s.handleListGroups)
	r.Post("/admin-groups", s.handleCreateGroup)
	r.Post("/admin-groups/{id}/members", s.handleAddMember)
	r.Delete("/admin-groups/{id}/members/{employeeId}", s.handleRemoveMember)
}

// pathID reads a UUID path parameter; a malformed one is simply not found.
func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, platform.ErrNotFound
	}
	return id, nil
}

func actor(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	p := actor(r)
	list, err := s.ListEmployees(r.Context(), p)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if seesFullDirectory(p) {
		platform.WriteJSON(w, http.StatusOK, map[string]any{"employees": list})
		return
	}
	basic := make([]basicEmployee, len(list))
	for i, e := range list {
		basic[i] = basicEmployee{e.ID, e.Email, e.FullName, e.Department, e.JobTitle, e.ManagerID}
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"employees": basic})
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email, FullName      string
		Department, JobTitle *string
		ManagerID            *uuid.UUID
	}
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	e, password, err := s.CreateEmployee(r.Context(), actor(r), NewEmployee{Email: in.Email, FullName: in.FullName,
		Department: in.Department, JobTitle: in.JobTitle, ManagerID: in.ManagerID})
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // carries a password
	platform.WriteJSON(w, http.StatusCreated, map[string]any{"employee": e, "temporaryPassword": password})
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	// Every field is required (null clears), so a forgotten key never clears a value.
	var raw struct{ Version, ManagerID, Department, JobTitle json.RawMessage }
	if err := platform.DecodeJSON(w, r, &raw); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in EmployeeUpdate
	for _, f := range []struct {
		name string
		raw  json.RawMessage
		dst  any
	}{{"version", raw.Version, &in.Version}, {"managerId", raw.ManagerID, &in.ManagerID},
		{"department", raw.Department, &in.Department}, {"jobTitle", raw.JobTitle, &in.JobTitle}} {
		if len(f.raw) == 0 {
			platform.WriteError(w, r, platform.ValidationError(f.name, "field_required"))
			return
		}
		if json.Unmarshal(f.raw, f.dst) != nil {
			platform.WriteError(w, r, platform.ValidationError(f.name, "invalid_json"))
			return
		}
	}
	e, err := s.UpdateEmployee(r.Context(), actor(r), id, in)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, e)
}

func (s *Service) handleReset(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	password, err := s.ResetPassword(r.Context(), actor(r), id)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // carries a password
	platform.WriteJSON(w, http.StatusOK, map[string]string{"temporaryPassword": password})
}

func (s *Service) handleAssignRole(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in struct{ Role string }
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	respondNoContent(w, r, s.AssignRole(r.Context(), actor(r), id, in.Role))
}

func (s *Service) handleRemoveRole(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	respondNoContent(w, r, s.RemoveRole(r.Context(), actor(r), id, chi.URLParam(r, "role")))
}

func respondNoContent(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.ListGroups(r.Context(), actor(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (s *Service) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	g, err := s.CreateGroup(r.Context(), actor(r), in.Name)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusCreated, g)
}

func (s *Service) handleAddMember(w http.ResponseWriter, r *http.Request) {
	group, err := pathID(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in struct{ EmployeeID uuid.UUID }
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	respondNoContent(w, r, s.AddMember(r.Context(), actor(r), group, in.EmployeeID))
}

func (s *Service) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	group, err := pathID(r, "id")
	if err == nil {
		var id uuid.UUID
		if id, err = pathID(r, "employeeId"); err == nil {
			err = s.RemoveMember(r.Context(), actor(r), group, id)
		}
	}
	respondNoContent(w, r, err)
}
