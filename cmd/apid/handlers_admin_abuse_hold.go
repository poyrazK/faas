package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// placeAccountAbuseHold is POST /v1/admin/accounts/{id}/abuse-hold: an
// operator holds an account (ADR-361). Boots and deploys are refused from
// the moment the row commits; schedd drains running workloads on its next
// reaper pass.
func (s *server) placeAccountAbuseHold(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.changeAccountAbuseHold(w, r, acct, true)
}

// releaseAccountAbuseHold is DELETE /v1/admin/accounts/{id}/abuse-hold. Apps
// stay parked and wake normally on their next request.
func (s *server) releaseAccountAbuseHold(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.changeAccountAbuseHold(w, r, acct, false)
}

func (s *server) changeAccountAbuseHold(w http.ResponseWriter, r *http.Request, operator state.Account, place bool) {
	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad account id", "expected UUID"))
		return
	}
	var req api.AccountAbuseHoldAction
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad JSON", err.Error()))
		return
	}
	if n := len(req.Note); n < 3 || n > 500 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad note", "note must be 3..500 characters"))
		return
	}
	holder, ok := s.store.(state.AccountAbuseHoldStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("account holds are not supported by this store"))
		return
	}
	var changed bool
	var err error
	if place {
		changed, err = holder.SetAccountAbuseHold(r.Context(), targetID, state.AccountAbuseHoldOperator, timeNow().UTC())
	} else {
		changed, err = holder.ReleaseAccountAbuseHold(r.Context(), targetID)
	}
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "no account with that id"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not change the account hold"))
		return
	}
	target, err := s.store.AccountByID(r.Context(), targetID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the account"))
		return
	}
	kind := "account.abuse_hold_released"
	if place {
		kind = "account.abuse_hold_placed"
	}
	if changed {
		tid := targetID
		s.audit.Emit(r.Context(), kind, &tid, map[string]any{
			"actor":       operator.ID,
			"actor_email": operator.Email,
			"note":        logsanitize.Field(req.Note),
			"at":          timeNow().UTC().Format(time.RFC3339Nano),
		})
		s.log.Warn("operator changed account abuse hold", "account", targetID, "placed", place, "actor", operator.ID)
	}
	writeJSON(w, http.StatusOK, api.AccountAbuseHoldActionResponse{AccountID: targetID, AbuseHold: accountAbuseHoldView(target), Changed: changed})
}
