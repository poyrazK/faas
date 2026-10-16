package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func uploadSubmissionAttemptID(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values(api.DeploymentAttemptHeader)
	if len(values) == 0 {
		return "", true
	}
	if len(values) != 1 || !api.ValidDeploymentAttemptID(values[0]) {
		api.WriteProblem(w, api.ErrValidation("Deployment-Attempt-ID must be 32 lowercase hexadecimal characters"))
		return "", false
	}
	return values[0], true
}
func (s *server) attachUploadSubmission(w http.ResponseWriter, r *http.Request, slug, deploymentID string) bool {
	id := r.Header.Get(api.DeploymentAttemptHeader)
	if id == "" {
		return true
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not recover upload deployment"))
		return false
	}
	store, ok := s.submissionStore(w)
	if !ok {
		return false
	}
	_, err = store.AttachDeploymentSubmission(r.Context(), app.ID, id, deploymentID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not retain upload deployment receipt"))
		return false
	}
	return true
}

// Replay the receipt before touching the upload again: a process may have died
// after atomic deployment acceptance but before marking its spool committed.
func (s *server) replayUploadSubmission(w http.ResponseWriter, r *http.Request, slug string) bool {
	id := r.Header.Get(api.DeploymentAttemptHeader)
	if id == "" {
		return false
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not recover upload app"))
		return true
	}
	store, ok := s.submissionStore(w)
	if !ok {
		return true
	}
	receipt, err := store.DeploymentSubmission(r.Context(), app.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify upload submission"))
		return true
	}
	s.replyAcceptedSubmission(w, r, app, receipt)
	return true
}
