package apidserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/state"
)

// internalSafeDeployRequestKey marks a request that arrived on a loopback
// Safe Deploy route. Handlers shared with customer routes use it to tell
// meterd's transitions from a person's (ADR-1447).
type internalSafeDeployRequestKey struct{}

func isInternalSafeDeployRequest(r *http.Request) bool {
	internal, _ := r.Context().Value(internalSafeDeployRequestKey{}).(bool)
	return internal
}

// internalRecordRolloutHold records why meterd is holding a canary step
// (ADR-1447). It never changes traffic or writes the deployment row.
func (s *server) internalRecordRolloutHold(w http.ResponseWriter, r *http.Request) {
	recorder, ok := s.store.(state.RolloutDecisionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("rollout decisions are unavailable"))
		return
	}
	d, err := s.store.DeploymentByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.internalSafeDeployLookupError(w, r, err, "deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil {
		s.internalSafeDeployLookupError(w, r, err, "deployment")
		return
	}
	var req api.RecordRolloutHoldRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	encoded, err := release.WorkerDecision(&req.Decision, api.RolloutDecisionHold)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid rollout decision", err.Error()))
		return
	}
	var accountID *uuid.UUID
	if parsed, err := uuid.Parse(app.AccountID); err == nil {
		accountID = &parsed
	}
	recorded, auditID, err := recorder.RecordRolloutHold(r.Context(), state.RolloutHoldParams{
		DeploymentID: d.ID, AccountID: accountID, ExpectedStep: req.ExpectedStep,
		Rule: req.Decision.Rule, Decision: encoded, Actor: canaryProgressionActor, At: time.Now().UTC(),
	})
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "record rollout hold",
			"Gregale could not record why this rollout is held.",
			"Retry in a moment; if it continues, contact support.", err)
		return
	}
	writeJSON(w, http.StatusOK, api.RecordRolloutHoldResponse{Recorded: recorded, AuditID: int64ToAuditIDString(auditID)})
}

// withRolloutHold fills RolloutHold on a single-deployment read while its
// rollout is in flight (ADR-1447). A read failure omits the field; the hold is
// an explanation, not state a client must act on.
func (s *server) withRolloutHold(ctx context.Context, resp api.DeploymentResponse) api.DeploymentResponse {
	reader, ok := s.store.(state.RolloutDecisionStore)
	rollout := strings.TrimSpace(resp.RolloutState)
	if !ok || resp.CanaryTotalSteps <= 0 || (rollout != "" && rollout != "pending" && rollout != "rolling_out") {
		return resp
	}
	raw, err := reader.LatestRolloutHold(ctx, resp.ID)
	if err != nil {
		s.log.Warn("apid: read rollout hold", "deployment", resp.ID, "err", err)
		return resp
	}
	resp.RolloutHold = release.DecodeDecision(raw)
	return resp
}
