// handlers_admin_force_park.go — operator-side recovery primitive
// P2a (force-park). PR #1099 P2 redesign: the handler no longer
// dials schedd over gRPC (that path violated the apid-control-
// plane-only depguard rule). It instead writes an
// operator_intents row (migrations/00431, status='pending'),
// emits db.NotifyOperatorIntent, and returns 202 Accepted with
// the intent_id + a status_url for polling. schedd is the only
// writer to instances; the trigger is now a Postgres row INSERT,
// and the actual Park runs in schedd's
// pkg/sched/operator_intent_subscriber.go dispatch path
// (which preserves the load-bearing lockApp +
// machine.go::CanTransition guard from §6.2).
//
// Auth + IDOR posture mirrors getAppMetrics (admin scope + MFA +
// s.adminAllows). Requires ?confirm=true as a tripwire — same
// pattern as /v1/compute-nodes/{name}/force-drain's --yes ack
// (commands_compute_nodes.go:249). Without confirm the handler
// returns 400 validation_failed so an operator can't fat-finger
// the call.
//
// Reason validation: a-z, 0-9, underscore only, ≤64 chars. The
// shape matches the audit log's parser at pkg/audit (which
// expects a slug-like token); non-conforming reasons are rejected
// with 400. Reason defaults to "operator_force_park" when the
// caller omits ?reason=.
//
// Audit row: operator.action.park_instance with
//
//	account_id = target instance's account id
//	data = {actor: caller.ID, intent_id, instance_id, app_id,
//	        deployment_id, previous_state, reason, result}
//
// `result` is "enqueued" when the intent row was written
// successfully (the durable record) or "rejected" when the
// state gate failed (the audit row is emitted even when no
// intent was written, so the operator's "I checked" is
// durable). Terminal outcome (succeeded/failed) is emitted by
// schedd as a separate operator.action.park_instance.outcome
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

// maxForceParkReasonLen caps the ?reason= query param so the
// audit row's data JSON stays bounded (matches the conventions at
// handlers_audit.go:255).
const maxForceParkReasonLen = 64

// forceParkReasonShape is the closed character class for
// ?reason=. a-z, 0-9, underscore only — same shape as the
// audit log's parser expects at pkg/audit. Other characters
// (whitespace, punctuation, emoji) are rejected at the handler
// so an operator typo doesn't survive into the audit log.
var forceParkReasonShape = regexp.MustCompile(`^[a-z0-9_]*$`)

// forceParkableStates is the closed set of instance states from
// which a force-park is allowed. Anything else (PARKED, PARKED_
// SCHEDULED, PARKED_FAILED, STOPPED, DELETED, …) returns 409
// instance_not_parkable WITHOUT writing an intent row.
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
var forceParkableStates = map[string]struct{}{
	string(state.StateRunning):     {},
	string(state.StateWaking):      {},
	string(state.StateColdBooting): {},
}

// operatorIntentPollHorizon is the recommended poll horizon
// stamped in the 202 Accepted response (ExpiresAt). 5 minutes
// gives the operator a comfortable buffer past the schedd
// safety tick (30s) + first dispatch attempt.
const operatorIntentPollHorizon = 5 * time.Minute

// postForcePark handles POST /v1/admin/instances/{id}/force-park.
// 202 on success (intent row written), 400 on missing
// ?confirm=true or invalid ?reason=, 403 admin_required, 404
// instance_not_found, 409 instance_not_parkable (no intent row
// written; audit row stamped with result="rejected").
func (s *server) postForcePark(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"confirm required",
			"?confirm=true is required to force-park an instance; aborts on operator typo"))
		return
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "operator_force_park"
	}
	if len(reason) > maxForceParkReasonLen || !forceParkReasonShape.MatchString(reason) {
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
	// Engine.Park — the schedd-side CanTransition guard closes
	// that race; the audit trail records both the gate-time
	// previous_state and the terminal outcome.
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
	if _, ok := forceParkableStates[ins.State]; !ok {
		// Reject WITHOUT writing an intent row. Audit row is
		// still emitted with result="rejected" so the operator
		// action is durable.
		emitOperatorActionParkInstance(r, s, acct, "", instanceID,
			ins.AppID, ins.DeploymentID, ins.State, reason,
			"rejected", "", nil)
		api.WriteProblem(w, api.NewProblem(http.StatusConflict,
			"instance_not_parkable",
			"instance is not in a parkable state",
			"current state: "+ins.State))
		return
	}

	// Resolve the app → account so the audit row's account_id is
	// the instance's owning account (not the calling admin's
	// account). If app resolution fails we still write the
	// intent row with account_id=NULL — the intent's
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
		state.OperatorIntentKindForcePark,
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
		"kind":      string(state.OperatorIntentKindForcePark),
		"target_id": instanceID,
	})
	if nerr := s.notif.Notify(r.Context(), db.NotifyOperatorIntent, string(notifyPayload)); nerr != nil {
		s.log.Warn("apid: operator_intent: notify failed",
			"intent_id", intentID, "err", nerr)
	}

	// Emit the request-kind audit row with result="enqueued"
	// + intent_id. The terminal outcome audit row is emitted
	// by schedd on terminal state (operator.action.park_instance.
	// outcome). Same actor + target shape as the previous
	// design; the rename of schedd_result→result reflects the
	// shift to async semantics (values: "enqueued" | "rejected").
	emitOperatorActionParkInstance(r, s, acct,
		targetAccountIDOrEmpty(targetAccountPtr),
		instanceID, ins.AppID, ins.DeploymentID, ins.State,
		reason, "enqueued", intentID, nil)

	writeJSON(w, http.StatusAccepted, api.OperatorIntentAcceptedResponse{
		OK:            true,
		IntentID:      intentID,
		StatusURL:     "/v1/admin/operator-intents/" + intentID,
		ExpiresAt:     time.Now().UTC().Add(operatorIntentPollHorizon),
		Kind:          string(state.OperatorIntentKindForcePark),
		InstanceID:    instanceID,
		PreviousState: ins.State,
		Reason:        reason,
	})
}

// targetAccountIDOrEmpty dereferences the optional pointer, "" when
// nil. Used to keep the audit row's account_id field consistent
// between the nil-pointer + non-empty-string shapes — the audit
// writer treats both the same way.
func targetAccountIDOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
