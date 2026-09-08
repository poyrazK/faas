// handlers_admin_force_restart.go — operator-side recovery primitive
// P2d (force-restart / kill instance + cold-boot on next wake).
// PR #1105 follow-on to PR #1099. Mirrors postForcePark + the
// `force_park` audit shape byte-for-byte with three differences:
//  1. State gate = forceRestartableStates ({RUNNING, WAKING,
//     COLD_BOOTING}) — same set as force-park. force-restart
//     is operator-initiated; it cannot act on PARKED / STOPPED
//     instances (the §6.2 CanTransition guard inside
//     schedd's Engine.ForceRestart fires on the locked re-read
//     and stamps the intent failed if a customer-driven Park
//     won the race).
//  2. Audit kind = operator.action.restart_instance (the
//     terminal outcome audit kind is
//     operator.action.restart_instance.outcome, emitted by
//     schedd's operator_intent_subscriber.go on the new
//     force_restart dispatch arm).
//  3. 409 code = instance_not_restartable (distinct from
//     instance_not_parkable so the audit-log filter can
//     distinguish the two rejection shapes).
//
// The handler does NOT call schedd over gRPC (that path violates
// the apid-control-plane-only depguard rule at
// .golangci.yml:41-58). It writes an operator_intents row
// (migrations/00446 widened the kind CHECK to include
// force_restart), fires pg_notify, and returns 202 Accepted.
// schedd is the only writer to instances; the trigger is now a
// Postgres row INSERT, and the actual destroy + snap-stale walk
// runs in schedd's operator_intent_subscriber.go dispatch path
// (which preserves the load-bearing lockApp + transitionWithKind
// guard from §6.2).
//
// Auth + IDOR posture mirrors postForcePark: admin scope + MFA +
// s.adminAllows (allowlist check). Requires ?confirm=true as a
// tripwire — same pattern as force-park's --yes ack
// (handlers_admin_force_park.go:97-103). Without confirm the
// handler returns 400 validation_failed so an operator can't
// fat-finger the call.
//
// Reason validation: a-z, 0-9, underscore only, ≤64 chars. Same
// shape as force-park (handlers_admin_force_park.go:60-66).
// Reason defaults to "operator_force_restart" when the caller
// omits ?reason=.
//
// Audit row: operator.action.restart_instance with
//
//	account_id = target instance's account id
//	data = {actor, intent_id, instance_id, app_id,
//	        deployment_id, previous_state, reason, result}
//
// `result` is "enqueued" when the intent row was written
// successfully (the durable record) or "rejected" when the
// state gate failed (the audit row is emitted even when no
// intent was written, so the operator's "I checked" is
// durable). Terminal outcome (succeeded/failed) is emitted by
// schedd as a separate operator.action.restart_instance.outcome
// audit row — see pkg/sched/operator_intent_subscriber.go.
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// maxForceRestartReasonLen caps the ?reason= query param so the
// audit row's data JSON stays bounded (same precedent as
// maxForceParkReasonLen at handlers_admin_force_park.go:59).
const maxForceRestartReasonLen = 64

// forceRestartReasonShape is the closed character class for
// ?reason=. a-z, 0-9, underscore only — same shape as the
// audit log's parser expects at pkg/audit. Other characters
// (whitespace, punctuation, emoji) are rejected at the handler
// so an operator typo doesn't survive into the audit log.
var forceRestartReasonShape = regexp.MustCompile(`^[a-z0-9_]*$`)

// forceRestartableStates is the closed set of instance states
// from which a force-restart is allowed. Only {RUNNING} is
// eligible — anything else (PARKED, PARKED_SCHEDULED, PARKED_
// FAILED, STOPPED, DELETED, WAKING, COLD_BOOTING, …) returns
// 409 instance_not_restartable WITHOUT writing an intent row.
//
// This is intentionally TIGHTER than forceParkableStates at
// handlers_admin_force_park.go:80-84 ({RUNNING, WAKING,
// COLD_BOOTING}). The engine's state-machine gate at
// pkg/sched/engine.go:5299 enforces the same {RUNNING}-only
// posture — Engine.ForceRestart returns
// state.ErrInstanceNotRunning on the locked re-read when the
// instance is in WAKING or COLD_BOOTING. If the apid gate
// accepted WAKING/COLD_BOOTING and the engine rejected them,
// the operator would see a misleading 202 Accepted (intent row
// written + pg_notify emitted) followed by a status=failed on
// polling — silent failure. Restricting the apid gate to
// {RUNNING} closes that race and matches the operator's actual
// semantic: "force-restart a wedged live instance".
//
// force-park (ParkInstance via Engine.Park) does not have
// this issue because the state machine at
// pkg/state/machine.go:62-63 explicitly allows all of
// {RUNNING, WAKING, COLD_BOOTING} → PARKED transitions, and
// Engine.ParkWithReason's CanTransition guard accepts them.
//
// Keyed by the raw instance.state string, built from the exported
// constants in pkg/state/machine.go.
//
// It used to hard-code "RUNNING" / "WAKING" / "COLD_BOOTING" on the
// stated grounds that "pkg/state does not export a State type today
// (the canonical constants live unexported in memstore.go)". That was
// not true — machine.go exports State along with StateRunning,
// StateWaking and StateColdBooting — and instances.state has been
// lowercase since instances_state_check in migration 00001. The gate
// therefore matched nothing and rejected every real instance.
var forceRestartableStates = map[string]struct{}{
	string(state.StateRunning): {},
}

// postForceRestart handles POST /v1/admin/instances/{id}/force-restart.
// 202 on success (intent row written), 400 on missing
// ?confirm=true or invalid ?reason=, 403 admin_required, 404
// instance_not_found, 409 instance_not_restartable (no intent
// row written; audit row stamped with result="rejected").
func (s *server) postForceRestart(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"confirm required",
			"?confirm=true is required to force-restart an instance; aborts on operator typo"))
		return
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "operator_force_restart"
	}
	if len(reason) > maxForceRestartReasonLen || !forceRestartReasonShape.MatchString(reason) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid reason",
			"reason must match [a-z0-9_]{1,64}"))
		return
	}

	instanceID := r.PathValue("id")
	if _, perr := uuid.Parse(instanceID); perr != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"instance not found", "instance id is not a valid uuid"))
		return
	}

	// State gate: read the instance fresh so the gate-time check
	// matches the audit row's previous_state. A racing customer-
	// driven Park can move the row between this read and schedd's
	// Engine.ForceRestart — the schedd-side CanTransition guard
	// (via locked re-read inside Engine.ForceRestart) closes that
	// race; the audit trail records both the gate-time
	// previous_state and the terminal outcome (succeeded/failed).
	ins, err := s.store.InstanceByID(r.Context(), instanceID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"instance not found", "no instance row with that id"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "instance read failed", err.Error()))
		return
	}
	if _, ok := forceRestartableStates[ins.State]; !ok {
		// Reject WITHOUT writing an intent row. Audit row is
		// still emitted with result="rejected" so the operator
		// action is durable.
		emitOperatorActionForceRestart(r, s, acct, "", instanceID,
			ins.AppID, ins.DeploymentID, ins.State, reason,
			"rejected", "", nil)
		api.WriteProblem(w, api.NewProblem(http.StatusConflict,
			"instance_not_restartable",
			"instance is not in a restartable state",
			"current state: "+ins.State))
		return
	}

	// Resolve the instance → app → account so the audit row's
	// account_id is the instance's owning account (not the calling
	// admin's account). If app resolution fails we still write
	// the intent row with account_id=NULL — the intent's
	// target_id is the source of truth for what the operator
	// acted on.
	app, aerr := s.store.AppByID(r.Context(), ins.AppID)
	var targetAccountPtr *string
	if aerr == nil && app.AccountID != "" {
		acctID := app.AccountID
		targetAccountPtr = &acctID
	}

	// Insert the intent row. This is the source of truth —
	// once it's durable, the request returns 202.
	intentID, err := s.store.InsertOperatorIntent(
		r.Context(),
		state.OperatorIntentKindForceRestart,
		instanceID,
		targetAccountPtr,
		acct.ID,
		reason,
		nil,
		middleware.TraceIDFrom(r),
	)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "insert operator intent failed", err.Error()))
		return
	}

	// Emit pg_notify. Fire-and-forget: a failure here is
	// logged but does NOT 5xx the request — the intent row
	// is durable and the 30s safety tick reclaims any row
	// whose notify was lost. Same precedent as
	// cmd/apid/handlers_cron_run.go:99-104.
	notifyPayload, _ := json.Marshal(map[string]any{
		"intent_id": intentID,
		"kind":      string(state.OperatorIntentKindForceRestart),
		"target_id": instanceID,
	})
	if nerr := s.notif.Notify(r.Context(), db.NotifyOperatorIntent, string(notifyPayload)); nerr != nil {
		s.log.Warn("apid: operator_intent: notify failed",
			"intent_id", intentID, "err", nerr)
	}

	// Emit the request-kind audit row with result="enqueued"
	// + intent_id. The terminal outcome audit row is emitted
	// by schedd on terminal state (operator.action.restart_instance
	// .outcome). Same actor + target shape as the force-park
	// handler; the kind field is "force_restart".
	emitOperatorActionForceRestart(r, s, acct,
		targetAccountIDOrEmpty(targetAccountPtr),
		instanceID, ins.AppID, ins.DeploymentID, ins.State,
		reason, "enqueued", intentID, nil)

	writeJSON(w, http.StatusAccepted, api.OperatorIntentAcceptedResponse{
		OK:            true,
		IntentID:      intentID,
		StatusURL:     "/v1/admin/operator-intents/" + intentID,
		ExpiresAt:     time.Now().UTC().Add(operatorIntentPollHorizon),
		Kind:          string(state.OperatorIntentKindForceRestart),
		InstanceID:    instanceID,
		PreviousState: ins.State,
		Reason:        reason,
	})
}
