package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) reviewBindingVerification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	backend, ok := s.verificationRunBackend(w, r, acct)
	if !ok {
		return
	}
	var in api.ReviewBindingVerificationRequest
	if err := decodeJSONSized(r, &in, api.BindingVerificationRunRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid verification review request"))
		return
	}
	review, err := backend.Review(r.Context(), in)
	if problem := bindings.VerificationRunProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, review)
}
func (s *server) reviewProjectBindingVerification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	backend, ok := s.projectVerificationRunBackend(w, r, acct)
	if !ok {
		return
	}
	var in api.ReviewProjectBindingVerificationRequest
	if err := decodeJSONSized(r, &in, api.ProjectBindingVerificationRunRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid graph verification review request"))
		return
	}
	review, err := backend.Review(r.Context(), in)
	if problem := bindings.VerificationRunProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, review)
}
