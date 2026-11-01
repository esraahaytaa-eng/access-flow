package access

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// handleGitHubStatus tells any signed-in employee whether this instance runs the optional
// GitHub sandbox adapter and for which tool, so the catalog and the tool page can say which
// service runs a tool (DECISIONS.md A1 note 2026-11-01, amended). It calls nothing (no GitHub
// request) and returns no setting but the tool, which the catalog already lists.
func (s *Service) handleGitHubStatus(w http.ResponseWriter, _ *http.Request) {
	var tool *uuid.UUID
	if s.GitHub != nil {
		tool = &s.GitHubTool
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"enabled": s.GitHub != nil, "toolId": tool})
}
