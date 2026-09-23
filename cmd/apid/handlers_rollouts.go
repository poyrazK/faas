// handlers_rollouts.go — apid handler for the operator manual
// rollout-recovery surface (issue #976 / ADR-122 / SAFE-RELEASES-R).
//
// Route (registered in server.go::handler):
//
//	POST /v1/apps/{slug}/rollouts/recover
//
// Body:
//
//	{"action": "advance"|"promote"|"abort",
//	 "reason": "<operator-supplied text>" }
//
// Response:
//
//	200 RolloutTransitionResponse{Deployment, AuditID}
//
// Trust model
//
//   - Plan-gate mirrors updateDeploymentTraffic / rollbackApp —
//     traffic-split recovery is Pro+ only
//     (acct.Plan.TrafficSplitAllowed()). Hobby / Free operators
//     running the CLI get a 403 plan_traffic_split_not_allowed
//     before the handler touches the store.
//
//   - State-machine guards run inside store.RecoverRollout so a
//     concurrent canary_progression tick or alert-driven action
//     executor can't interleave a partial state. The handler
//     translates the closed-set errors (ErrInvalidRecoverAction,
//     ErrRolloutNotStuck, ErrRolloutStateInvalid) to the canonical
//     422/409 problem shapes.
//
//   - Audit emit is best-effort after the deployment stamp
//     commits; the store writes the deployment_audit row inside
//     the same tx as the deployment stamp, so the audit chain is
//     always consistent with the deployment row. The handler's
//     post-write audit.Emit logs the operator's actor to the
//     events stream so SOC 2 / GDPR auditors can re-derive who
//     tripped the recovery.

package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

// recoverRollout is the handler body mounted at
// POST /v1/apps/{slug}/rollouts/recover.
//
// Phase order:
//
//  1. Plan-gate (Pro+ only) — same posture as
//     updateDeploymentTraffic. 403 plan_traffic_split_not_allowed.
//  2. Decode body — 400 malformed.
//  3. Action closed-set — 422 invalid_recover_action.
//  4. loadApp — 404 no such app, 403 cross-account.
//  5. Reason trim — empty is legal (the operator might want to
//     trip a default reason; the meterd audit row records "").
//  6. store.RecoverRollout — atomic-tx with state-machine guards
//     (advance/promote/abort). Returns refreshed Deployment +
//     audit row id.
//  7. s.audit.Emit — best-effort events stream side-channel.
//  8. 200 RolloutTransitionResponse.
func (s *server) recoverRollout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// (1) Plan-tier gate (Pro+). Mirrors handlers_ext.go:1504.
	if !acct.Plan.TrafficSplitAllowed() {
		api.WriteProblem(w, api.ErrPlanTrafficSplitNotAllowed(acct.Plan))
		return
	}
	// (2) Decode body.
	var req api.RecoverRolloutRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	// (3) Action closed-set. The store re-validates; this is
	// the primary gate so the operator's 422 surfaces
	// without a Postgres round-trip.
	if !api.AllowedRecoverRolloutAction(req.Action) {
		api.WriteProblem(w, api.ErrInvalidRecoverAction(req.Action))
		return
	}
	// (4) loadApp — IDOR-safe lookup (404 unknown, 403
	// cross-account). This follows request-shape validation so
	// malformed/unknown actions have deterministic client errors
	// even when the slug is stale.
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	// (5) Reason trim (allow empty). The cap is in BYTES, not characters —
	// it stands in for a Postgres column width. Cutting with a byte slice
	// would split a multi-byte rune and produce invalid UTF-8, which the
	// text and jsonb writes downstream both reject (SQLSTATE 22021 / 22P02),
	// rolling back the recovery this endpoint exists to perform.
	req.Reason = safetext.Truncate(req.Reason, api.AuditReasonMaxBytes)
	// (6) Atomic-tx recovery.
	updated, auditID, err := s.store.RecoverRollout(r.Context(), app.ID, req.Action, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no active rollout for this app")
		case errors.Is(err, state.ErrInvalidRecoverAction):
			// Defence-in-depth — the closed-set check above is
			// the primary gate, but a programmatic caller
			// bypassing the handler gets the same 422.
			api.WriteProblem(w, api.ErrInvalidRecoverAction(req.Action))
		case errors.Is(err, state.ErrRolloutNotStuck):
			api.WriteProblem(w, api.ErrRolloutNotStuck())
		case errors.Is(err, state.ErrRolloutStateInvalid):
			api.WriteProblem(w, api.ErrRolloutStateInvalid(updated.RolloutState))
		default:
			writeCustomerInternalProblem(w, r, s.log, "recover rollout",
				"Gregale could not recover this rollout.",
				"Retry the request in a moment; if it continues, contact support.", err)
		}
		return
	}
	// (7) Audit emit (events stream). The store already wrote
	// the deployment_audit row inside the same tx; this is the
	// events-stream side-channel so SOC 2 / GDPR auditors can
	// re-derive the post-recovery state via the unified events
	// feed. Best-effort: a failure is logged-and-continued.
	if s.audit != nil {
		s.audit.Emit(r.Context(), "deployment.rollout_recovered", &acct.ID, map[string]any{
			"app":              app.ID,
			"deployment":       updated.ID,
			"action":           req.Action,
			"reason":           req.Reason,
			"deployment_audit": auditID,
			"actor":            acct.ID,
		})
	}
	status := http.StatusOK
	if state.IsServiceRollout(updated) && updated.ServiceRolloutHandoff.ActiveAbort() {
		status = http.StatusAccepted
		if s.notif != nil {
			payload, _ := json.Marshal(map[string]any{
				"kind": "service_rollout_abort", "status": string(updated.Status),
				"app_id": app.ID, "deployment_id": updated.ID,
			})
			if err := s.notif.Notify(r.Context(), db.NotifyDeploymentChanged, string(payload)); err != nil {
				s.log.Warn("apid: notify service rollout abort failed", "app", app.ID, "deployment", updated.ID, "err", err)
			}
		}
	}
	// (8) RolloutTransitionResponse. A service abort is asynchronous and
	// therefore returns 202 while its durable handoff remains rolling_out.
	// audit id on the operator's terminal.
	writeJSON(w, status, api.RolloutTransitionResponse{
		Deployment: s.deploymentResponse(updated, app),
		AuditID:    int64ToAuditIDString(auditID),
	})
}

// int64ToAuditIDString renders the deployment_audit id the
// store returned into the JSON-safe string the wire DTO
// expects (RolloutTransitionResponse.AuditID). Caller can echo
// this on the operator's terminal as `audit_id=…`. Kept as a
// helper so the json:"audit_id" field stays `string` (wire
// stable across the Go → JS / Go → Python SDK round-trips —
// JSON number → int64 → BigInt drift is a known footgun).
func int64ToAuditIDString(id int64) string {
	if id == 0 {
		return ""
	}
	const digits = "0123456789"
	if id < 0 {
		// Should be unreachable (identity column), but
		// render a stable sentinel so the CLI surfaces the
		// anomaly instead of a silent zero.
		return "invalid"
	}
	var buf [20]byte
	i := len(buf)
	for id > 0 {
		i--
		buf[i] = digits[id%10]
		id /= 10
	}
	return string(buf[i:])
}
