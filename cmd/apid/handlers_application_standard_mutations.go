package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// The opt-in is a release gate, not evidence that consumer or native acceptance
// has passed. Deployment must keep it unset until ADR-435's gates are complete.
func applicationStandardMutationsEnabledFromEnv(getenv func(string) string) bool {
	return strings.TrimSpace(getenv("FAAS_APPLICATION_STANDARD_MUTATIONS_ENABLED")) == "1"
}

func (s *server) WithApplicationStandardMutationsEnabled(enabled bool) *server {
	s.applicationStandardMutationsEnabled = enabled
	return s
}

// Gate before idempotency lookup: disabling admission also disables replay of a
// previously successful mutation response on this replica.
func (s *server) requireApplicationStandardMutations(action authz.OrgAction, next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !s.applicationStandardMutationsAvailable(w) {
			return
		}
		if _, _, ok := s.applicationStandardMutationApp(w, r, action); !ok {
			return
		}
		next(w, r, acct)
	}
}

func (s *server) applicationStandardMutationsAvailable(w http.ResponseWriter) bool {
	if !s.applicationStandardMutationsEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeApplicationStandardsPending, "Application standards mutations disabled", "Application standards mutations are not enabled on this control-plane host."))
		return false
	}
	return true
}

func (s *server) applicationStandardMutationApp(w http.ResponseWriter, r *http.Request, action authz.OrgAction) (string, string, bool) {
	_, orgID, ok := s.applicationStandardsStore(w, r, action)
	if !ok {
		return "", "", false
	}
	app, ok := s.applicationStandardLiveApp(w, r, orgID)
	return orgID, app.ID, ok
}

func (s *server) setApplicationStandardLocalIntent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	orgID, appID, ok := s.applicationStandardMutationApp(w, r, authz.OrgActionSetApplicationStandardLocalIntent)
	if !ok {
		return
	}
	var req api.SetApplicationStandardLocalIntentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid application standard local intent body"))
		return
	}
	store, ok := s.store.(state.ApplicationStandardLocalIntentStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standards local intent is unavailable"))
		return
	}
	enrollment, err := store.SetApplicationStandardLocalIntent(r.Context(), orgID, acct.ID, appID, state.ApplicationStandardLocalIntentRequest(req))
	if err != nil {
		writeApplicationStandardMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, applicationStandardEnrollmentResponse(enrollment))
}

func (s *server) approveApplicationStandardException(w http.ResponseWriter, r *http.Request, acct state.Account) {
	orgID, appID, ok := s.applicationStandardMutationApp(w, r, authz.OrgActionApproveApplicationStandards)
	if !ok {
		return
	}
	var req api.ApproveApplicationStandardExceptionRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid application standard exception approval body"))
		return
	}
	store, ok := s.store.(state.ApplicationStandardExceptionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standards exceptions are unavailable"))
		return
	}
	x, err := store.ApproveApplicationStandardException(r.Context(), orgID, acct.ID, appID, state.ApplicationStandardExceptionRequest(req))
	if err != nil {
		writeApplicationStandardMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, applicationStandardExceptionResponse(x, time.Now().UTC()))
}

func (s *server) revokeApplicationStandardException(w http.ResponseWriter, r *http.Request, acct state.Account) {
	orgID, appID, ok := s.applicationStandardMutationApp(w, r, authz.OrgActionApproveApplicationStandards)
	if !ok {
		return
	}
	id, ok := standardPathUUID(w, r, "exception")
	if !ok {
		return
	}
	var req api.RevokeApplicationStandardExceptionRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid application standard exception revocation body"))
		return
	}
	store, ok := s.store.(state.ApplicationStandardExceptionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standards exceptions are unavailable"))
		return
	}
	x, err := store.RevokeApplicationStandardException(r.Context(), orgID, acct.ID, appID, id, req.ExpectedRevision)
	if err != nil {
		writeApplicationStandardMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, applicationStandardExceptionResponse(x, time.Now().UTC()))
}

func writeApplicationStandardMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrApplicationStandardReviewForbidden):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeOrgRoleForbidden, "Application standards mutation forbidden", "An active organization membership with the required application standards action is required."))
	case errors.Is(err, state.ErrApplicationStandardLocalIntentStale), errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardVersionStale, "Application standards inputs changed", "Refresh the enrollment revision and exception history before retrying."))
	case errors.Is(err, state.ErrApplicationStandardsPending), errors.Is(err, state.ErrApplicationStandardReviewBusy), errors.Is(err, state.ErrApplicationStandardOperationInProgress):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardsPending, "Application standards change pending", "Retry after the current installation or controlled operation finishes."))
	case errors.Is(err, state.ErrApplicationStandardReviewBlocked):
		api.WriteProblem(w, api.ErrValidation("application standard constraints or platform controls refuse the requested change"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid application standard mutation parameters"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Application standards resource not found", "The requested resource is unavailable in this application and organization."))
	default:
		api.WriteProblem(w, api.ErrCapacity("application standard mutation failed"))
	}
}
