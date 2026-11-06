package objectstorage

import (
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/state"
)

// Uploads and receipt reads share this check. AuthenticateKey resolves the
// credential through apid but does not load the API middleware's project limit.
// Read the immutable restriction at the edge, including during version skew.
func (h *uploadHandler) authorizeUploadProjects(w http.ResponseWriter, r *http.Request, app state.App, key state.APIKey) bool {
	if !key.ProjectLimitable() {
		return true
	}
	reader, ok := h.store.(state.APIKeyProjectLimitReader)
	if !ok {
		uploadProblem(w, http.StatusServiceUnavailable, "upload authorization is temporarily unavailable")
		return false
	}
	projects, limited, err := reader.APIKeyProjectLimit(r.Context(), key.ID)
	if err != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "upload authorization is temporarily unavailable")
		return false
	}
	if limited && (app.ProjectID == "" || !slices.Contains(projects, app.ProjectID)) {
		uploadProblem(w, http.StatusForbidden, "the presented API key is limited to other projects")
		return false
	}
	return true
}
