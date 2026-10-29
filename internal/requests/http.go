package requests

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Register adds the request and approval routes to r, which must already require a session.
func (s *Service) Register(r chi.Router) {
	r.Post("/requests", s.handleSubmit)
	r.Get("/requests", s.handleList)
	r.Get("/requests/{id}", s.handleGet)
	r.Post("/request-items/{id}/withdraw", s.handleWithdraw)
	r.Get("/approvals", s.handleInbox)
	r.Get("/approvals/open", s.handleOpen)
	r.Post("/approvals/{id}/approve", s.handleApprove)
	r.Post("/approvals/{id}/reject", s.handleReject)
	r.Post("/approvals/{id}/reassign", s.handleReassign)
	r.Post("/approvals/{id}/override", s.handleOverride)
	r.Get("/email-outbox", s.handleOutbox)
	r.Get("/exceptions", s.handleExceptions)
	r.Get("/audit/requests", s.handleAudited)
	r.Post("/direct-grants", s.handleDirectGrant)
	r.Post("/grants/{id}/change", s.handleChange)
	r.Post("/employees/{id}/suspend", s.lifecycleRoute(s.Suspend))
	r.Post("/employees/{id}/reactivate", s.lifecycleRoute(s.Reactivate))
	r.Post("/employees/{id}/terminate", s.lifecycleRoute(s.Terminate))
	r.Post("/employees/{id}/transfer", s.handleTransfer)
	r.Put("/employees/{id}/termination", s.handleSchedule)
	r.Post("/employees/{id}/termination/cancel", s.lifecycleRoute(s.CancelTermination))
}

func actor(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func (s *Service) handleSubmit(w http.ResponseWriter, r *http.Request) {
	type itemBody struct {
		AccessLevelID *uuid.UUID
		Justification *string
		Duration      *Duration
	}
	var in struct {
		SubmissionID  *uuid.UUID
		BeneficiaryID *uuid.UUID
		Items         *[]itemBody
	}
	err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "submissionId", Present: in.SubmissionID != nil}, {Name: "items", Present: in.Items != nil}}
	})
	sub := Submission{BeneficiaryID: in.BeneficiaryID}
	if err == nil {
		sub.SubmissionID = *in.SubmissionID
		for i, it := range *in.Items {
			for _, f := range []platform.Field{{Name: "accessLevelId", Present: it.AccessLevelID != nil},
				{Name: "justification", Present: it.Justification != nil}, {Name: "duration", Present: it.Duration != nil}} {
				if !f.Present && err == nil {
					err = platform.ValidationError(fmt.Sprintf("items[%d].%s", i, f.Name), "field_required")
				}
			}
			if err == nil {
				sub.Items = append(sub.Items, ItemInput{*it.AccessLevelID, *it.Justification, *it.Duration})
			}
		}
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	req, created, err := s.Submit(r.Context(), actor(r), sub)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	platform.Respond(w, r, status, req, err)
}

func (s *Service) handleDirectGrant(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SubmissionID  *uuid.UUID
		BeneficiaryID *uuid.UUID
		AccessLevelID *uuid.UUID
		Justification *string
		ExpiresOn     *string
	}
	err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "submissionId", Present: in.SubmissionID != nil}, {Name: "beneficiaryId", Present: in.BeneficiaryID != nil},
			{Name: "accessLevelId", Present: in.AccessLevelID != nil}, {Name: "justification", Present: in.Justification != nil}}
	})
	var req Request
	created := false
	if err == nil {
		req, created, err = s.DirectGrant(r.Context(), actor(r), DirectGrantInput{*in.SubmissionID, *in.BeneficiaryID,
			*in.AccessLevelID, *in.Justification, in.ExpiresOn})
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	platform.Respond(w, r, status, req, err)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	reqs, err := s.List(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"requests": reqs}, err)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	var req Request
	if err == nil {
		req, err = s.Get(r.Context(), actor(r), ids[0])
	}
	platform.Respond(w, r, http.StatusOK, req, err)
}

func (s *Service) handleInbox(w http.ResponseWriter, r *http.Request) {
	list, err := s.Inbox(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"approvals": list}, err)
}

func (s *Service) handleOpen(w http.ResponseWriter, r *http.Request) {
	list, err := s.Open(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"approvals": list}, err)
}

func (s *Service) handleExceptions(w http.ResponseWriter, r *http.Request) {
	ex, err := s.Exceptions(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, ex, err)
}

func (s *Service) handleOutbox(w http.ResponseWriter, r *http.Request) {
	emails, err := s.Outbox(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"emails": emails}, err)
}

// action is the body of a step or item action: the version, and a reason where needed.
type action struct {
	Version    *int
	Reason     *string
	AssigneeID *uuid.UUID
}

// decodeAction reads the path ID and an action body requiring version and the named fields.
func decodeAction(w http.ResponseWriter, r *http.Request, required ...string) (uuid.UUID, action, error) {
	var in action
	ids, err := platform.PathIDs(r, "id")
	if err != nil {
		return uuid.Nil, in, err
	}
	err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
		present := map[string]bool{"version": in.Version != nil, "reason": in.Reason != nil, "assigneeId": in.AssigneeID != nil}
		fields := []platform.Field{{Name: "version", Present: present["version"]}}
		for _, name := range required {
			fields = append(fields, platform.Field{Name: name, Present: present[name]})
		}
		return fields
	})
	if in.Reason == nil {
		in.Reason = new(string)
	}
	return ids[0], in, err
}

func (s *Service) handleApprove(w http.ResponseWriter, r *http.Request) {
	id, in, err := decodeAction(w, r)
	var it Item
	if err == nil {
		it, err = s.Approve(r.Context(), actor(r), id, *in.Version)
	}
	platform.Respond(w, r, http.StatusOK, it, err)
}

func (s *Service) handleReject(w http.ResponseWriter, r *http.Request) {
	id, in, err := decodeAction(w, r, "reason")
	var it Item
	if err == nil {
		it, err = s.Reject(r.Context(), actor(r), id, *in.Version, *in.Reason)
	}
	platform.Respond(w, r, http.StatusOK, it, err)
}

func (s *Service) handleReassign(w http.ResponseWriter, r *http.Request) {
	id, in, err := decodeAction(w, r, "assigneeId", "reason")
	var it Item
	if err == nil {
		it, err = s.Reassign(r.Context(), actor(r), id, *in.Version, *in.AssigneeID, *in.Reason)
	}
	platform.Respond(w, r, http.StatusOK, it, err)
}

func (s *Service) handleOverride(w http.ResponseWriter, r *http.Request) {
	id, in, err := decodeAction(w, r, "reason")
	var it Item
	if err == nil {
		it, err = s.Override(r.Context(), actor(r), id, *in.Version, *in.Reason)
	}
	platform.Respond(w, r, http.StatusOK, it, err)
}

func (s *Service) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	id, in, err := decodeAction(w, r, "reason")
	var it Item
	if err == nil {
		it, err = s.Withdraw(r.Context(), actor(r), id, *in.Version, *in.Reason)
	}
	platform.Respond(w, r, http.StatusOK, it, err)
}
