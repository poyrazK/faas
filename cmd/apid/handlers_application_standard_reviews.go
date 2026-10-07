package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewApplicationStandardAssignment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionManageApplicationStandards)
	if !ok {
		return
	}
	var req api.ApplicationStandardReviewRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid application standard review body"))
		return
	}
	store, ok := s.store.(state.ApplicationStandardReviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standard reviews are unavailable"))
		return
	}
	plan, err := store.PreviewApplicationStandardAssignment(r.Context(), orgID, acct.ID, state.ApplicationStandardReviewRequest(req))
	if err != nil {
		writeApplicationStandardReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, applicationStandardReviewResponse(plan))
}

func (s *server) getApplicationStandardReview(w http.ResponseWriter, r *http.Request, _ state.Account) {
	_, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	id, ok := standardPathUUID(w, r, "review")
	if !ok {
		return
	}
	store, ok := s.store.(state.ApplicationStandardReviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standard reviews are unavailable"))
		return
	}
	plan, err := store.GetApplicationStandardReviewPlan(r.Context(), orgID, id)
	if err != nil {
		writeApplicationStandardReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, applicationStandardReviewResponse(plan))
}

func (s *server) getApplicationStandardOperation(w http.ResponseWriter, r *http.Request, _ state.Account) {
	_, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	id, ok := standardPathUUID(w, r, "operation")
	if !ok {
		return
	}
	store, ok := s.store.(state.ApplicationStandardOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standard operations are unavailable"))
		return
	}
	operation, err := store.GetApplicationStandardOperation(r.Context(), orgID, id)
	if err != nil {
		writeApplicationStandardReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, applicationStandardOperationResponse(operation))
}

func (s *server) listApplicationStandardExceptions(w http.ResponseWriter, r *http.Request, _ state.Account) {
	_, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	app, ok := s.applicationStandardLiveApp(w, r, orgID)
	if !ok {
		return
	}
	after, limit, ok := applicationStandardUUIDPage(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.ApplicationStandardExceptionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standard exceptions are unavailable"))
		return
	}
	rows, err := store.ListApplicationStandardExceptions(r.Context(), orgID, app.ID, after)
	if err != nil {
		writeApplicationStandardReviewError(w, err)
		return
	}
	result := api.ApplicationStandardExceptionList{Exceptions: []api.ApplicationStandardException{}, AsOf: time.Now().UTC()}
	if len(rows) >= limit {
		result.NextPageAfter = rows[limit-1].ID
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for _, x := range rows {
		result.Exceptions = append(result.Exceptions, applicationStandardExceptionResponse(x, result.AsOf))
	}
	writeJSON(w, http.StatusOK, result)
}

func applicationStandardUUIDPage(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	after, limit := r.URL.Query().Get("after"), api.ApplicationStandardMaxListPage
	if after != "" {
		id, err := uuid.Parse(after)
		if err != nil || id == uuid.Nil {
			api.WriteProblem(w, api.ErrValidation("after must be a nonzero UUID"))
			return "", 0, false
		}
		after = id.String()
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > api.ApplicationStandardMaxListPage {
			api.WriteProblem(w, api.ErrValidation("limit is outside the application standard page bounds"))
			return "", 0, false
		}
	}
	return after, limit, true
}

func writeApplicationStandardReviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrApplicationStandardReviewForbidden):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Review forbidden", "An active organization owner or administrator is required."))
	case errors.Is(err, state.ErrApplicationStandardReviewStale), errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardVersionStale, "Review inputs changed", "Refresh the assignment and preview the current inputs."))
	case errors.Is(err, state.ErrApplicationStandardReviewBusy):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardsPending, "Review inputs busy", "Retry after the current change finishes."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid application standard review parameters"))
	default:
		writeApplicationStandardError(w, err)
	}
}
