// handlers_admin_operator_intent.go — operator polling endpoint
// for the P2 redesign (PR #1099). GET /v1/admin/operator-intents/{id}
// returns the current state of an intent row from operator_intents
// (migrations/00431). Status is one of "pending" | "running" |
// "succeeded" | "failed" | "cancelled".
//
// Auth: admin scope (NO MFA — mirrors the getFireCronRequest
// pattern at cmd/apid/handlers_fire_cron_request.go:38-83
// because the operator is already authenticated via the
// initial POST that created the intent; MFA would be a
// re-auth at the polling endpoint with no operational
// benefit).
//
// IDOR closure: returns 404 (not 403) when the row is not
// found OR belongs to an account the caller is not allowed
// to see. The byte-identical posture prevents an admin from
// distinguishing "wrong id" from "wrong owner" by status
// code alone. Fleet-level intents (account_id=NULL) are
// visible to any admin — the admin scope gate is the only
// load-bearing access control on this endpoint.
//
// Response shape: api.OperatorIntentResponse, mirrors
// state.OperatorIntent directly.
package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getOperatorIntent handles GET /v1/admin/operator-intents/{id}.
// 200 with full DTO on found, 404 on missing-or-wrong-owner,
// 400 on invalid id shape.
func (s *server) getOperatorIntent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"operator_intent_not_found", "id is not a valid uuid"))
		return
	}
	intent, err := s.store.GetOperatorIntent(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrOperatorIntentNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"operator_intent_not_found", "no operator intent with that id"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "read operator intent failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, toOperatorIntentResponse(intent))
}

func toOperatorIntentResponse(intent state.OperatorIntent) api.OperatorIntentResponse {
	return api.OperatorIntentResponse{
		IntentID:           intent.ID,
		Kind:               string(intent.Kind),
		Status:             string(intent.Status),
		TargetID:           intent.TargetID,
		AccountID:          stringOrEmpty(intent.AccountID),
		ActorID:            intent.ActorID,
		Reason:             intent.Reason,
		Metadata:           intent.Metadata,
		TraceID:            stringOrEmpty(intent.TraceID),
		RequestedAt:        intent.RequestedAt,
		StartedAt:          intent.StartedAt,
		FinishedAt:         intent.FinishedAt,
		Error:              intent.Error,
		SnapIDsMarkedStale: intent.SnapIDsMarkedStale,
	}
}

func stringOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
