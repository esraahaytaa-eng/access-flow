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
	r.Post("/employees/{id}/rehire", s.handleRehire)
	r.Post("/employees/{id}/roles", s.handleAssignRole)
	r.Delete("/employees/{id}/roles/{role}", s.handleRemoveRole)
	r.Get("/company-catalog", s.handleCatalog)
	r.Post("/company-catalog/{list}", s.handleAddEntry)
	r.Post("/company-catalog/{list}/{id}/retire", s.handleRetireEntry)
	r.Get("/admin-groups", s.handleListGroups)
	r.Post("/admin-groups", s.handleCreateGroup)
	r.Post("/admin-groups/{id}/members", s.handleAddMember)
	r.Delete("/admin-groups/{id}/members/{employeeId}", s.handleRemoveMember)
	r.Get("/super-admin-nominations", s.handleNominations)
	r.Post("/super-admin-nominations", s.handleNominate)
	r.Post("/super-admin-nominations/{id}/authorize", s.handleDecide(true))
	r.Post("/super-admin-nominations/{id}/decline", s.handleDecide(false))
	r.Post("/super-admin-nominations/{id}/reissue", s.handleReissue)
	r.Post("/super-admin-nominations/accept", s.handleAccept)
	r.Get("/admin/health", s.handleHealth)
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
	e, err := s.CreateEmployee(r.Context(), actor(r), NewEmployee{Email: in.Email, FullName: in.FullName,
		Department: in.Department, JobTitle: in.JobTitle, ManagerID: in.ManagerID})
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusCreated, map[string]any{"employee": e})
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
	if err := platform.DecodeRaw([]platform.RawField{{Name: "version", Raw: raw.Version, Dst: &in.Version}, {Name: "managerId", Raw: raw.ManagerID, Dst: &in.ManagerID},
		{Name: "department", Raw: raw.Department, Dst: &in.Department}, {Name: "jobTitle", Raw: raw.JobTitle, Dst: &in.JobTitle}}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	e, err := s.UpdateEmployee(r.Context(), actor(r), id, in)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, e)
}

func (s *Service) handleRehire(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var raw struct{ Version, ManagerID, Department, JobTitle, Reason json.RawMessage }
	if err := platform.DecodeJSON(w, r, &raw); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var in RehireInput
	if err := platform.DecodeRaw([]platform.RawField{{Name: "version", Raw: raw.Version, Dst: &in.Version}, {Name: "managerId", Raw: raw.ManagerID, Dst: &in.ManagerID},
		{Name: "department", Raw: raw.Department, Dst: &in.Department}, {Name: "jobTitle", Raw: raw.JobTitle, Dst: &in.JobTitle}, {Name: "reason", Raw: raw.Reason, Dst: &in.Reason}}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	e, err := s.Rehire(r.Context(), actor(r), id, in)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"employee": e})
}

func (s *Service) handleReset(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if err := s.IssueActivationLink(r.Context(), actor(r), id); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (s *Service) handleNominations(w http.ResponseWriter, r *http.Request) {
	list, err := s.Nominations(r.Context(), actor(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"nominations": list})
}

func (s *Service) handleNominate(w http.ResponseWriter, r *http.Request) {
	var in struct{ NomineeID *uuid.UUID }
	if err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "nomineeId", Present: in.NomineeID != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	n, err := s.Nominate(r.Context(), actor(r), *in.NomineeID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusCreated, n)
}

func (s *Service) handleDecide(authorize bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		var in struct {
			Version *int
			Reason  *string
		}
		if err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
			return []platform.Field{{Name: "version", Present: in.Version != nil}}
		}); err != nil {
			platform.WriteError(w, r, err)
			return
		}
		reason, err := CleanText(in.Reason, "reason")
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		n, err := s.Decide(r.Context(), actor(r), id, *in.Version, authorize, reason)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, n)
	}
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	h, err := s.Health(r.Context(), actor(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, h)
}

func (s *Service) handleReissue(w http.ResponseWriter, r *http.Request) {
	var in struct{ Version *int }
	id, err := pathID(r, "id")
	if err == nil {
		err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
			return []platform.Field{{Name: "version", Present: in.Version != nil}}
		})
	}
	var n Nomination
	if err == nil {
		n, err = s.ReissueRoleLink(r.Context(), actor(r), id, *in.Version)
	}
	platform.Respond(w, r, http.StatusOK, n, err)
}

func (s *Service) handleAccept(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token *string }
	err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "token", Present: in.Token != nil}}
	})
	if err == nil {
		err = s.AcceptRole(r.Context(), actor(r), *in.Token)
	}
	platform.Respond(w, r, http.StatusOK, map[string]bool{"accepted": err == nil}, err)
}
