// handlers_queue_controls.go — ADR-124 deployment queue controls.
//
// Four handlers covering cancel / reorder / clear (single) /
// clear-obsolete. Each handler is ≤50 lines per CLAUDE.md
// ("Handlers ≤ 50 lines — extract"). The pgstore-layer state
// machine + CAS guards live in pkg/state/pgstore.go near
// MarkDeploymentSuperseded (:5092); these handlers do no
// state-machine policy and only translate store sentinels to
// the canonical RFC 7807 problem.
//
// The {id} → {app → account} IDOR check is the same pattern as
// updateDeploymentMinInstances at handlers_ext.go:1320.
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// buildChangedPayload is the wire shape for db.NotifyBuildChanged
// (matched by the decoder in cmd/builderd/main.go's
// handleBuildCancelled). Keep field names in sync with the doc
// comment on db/notify.go for the same channel.
type buildChangedPayload struct {
	BuildID      string `json:"build_id"`
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
	Cascade      bool   `json:"cascade"`
}

// resolveDeploymentAccount is the cross-handler IDOR gate. It
// loads the deployment row, the parent app, and asserts the
// caller's account owns the app. Returns the deployment + app on
// success; writes a 404 on every failure (a path-id exists but
// belongs to another account is indistinguishable from "no such
// row" — same response, same body). Used by all four handlers.
func (s *server) resolveDeploymentAccount(w http.ResponseWriter, r *http.Request, acct state.Account, id string) (state.Deployment, state.App, bool) {
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return state.Deployment{}, state.App{}, false
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return state.Deployment{}, state.App{}, false
	}
	return d, app, true
}

// handleCancelDeployment — POST /v1/apps/{slug}/deployments/{id}/cancel.
//
// Body: {"reason"?: "user"|"auto_quota"|"auto_health"|"system"}
// 200 → {id, status:"cancelled", cancelled_at}; the pg_notify
// deployment_changed payload fires from the apidsource path after
// commit. Live deployments return 409 with the canonical
// "use deploys rollback" hint. Stops at the deployment row flip —
// the Firecracker VM tear-down is the builderd cancel-LISTEN
// goroutine's job (pkg/builderd/builderd.go).
func (s *server) handleCancelDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	principal := acct.ID
	reason := state.CancelReasonUser
	if r.ContentLength > 0 {
		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req) // tolerate empty body
		if req.Reason != "" {
			parsed := state.CancelReason(req.Reason)
			if !parsed.IsValid() {
				api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
					"invalid cancel_reason",
					"reason must be one of user|auto_quota|auto_health|system"))
				return
			}
			reason = parsed
		}
	}
	if _, _, ok := s.resolveDeploymentAccount(w, r, acct, id); !ok {
		return
	}
	d, cancelledBuilds, err := s.store.CancelDeploymentTx(r.Context(), id, principal, reason)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such deployment")
	case errors.Is(err, state.ErrCancelLiveForbidden):
		s.ops.ObserveDeploymentCancelled("live_forbidden")
		api.WriteProblem(w, api.ErrDeploymentCancelLiveForbidden(id))
	case errors.Is(err, state.ErrInvalidStateTransition):
		s.ops.ObserveDeploymentCancelled("not_cancellable")
		api.WriteProblem(w, api.ErrDeploymentCancelNotCancellable(id))
	case err != nil:
		s.ops.ObserveDeploymentCancelled("error")
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "cancel failed", err.Error()))
	default:
		// ADR-124: fire one build_changed pg_notify per cascade-cancelled
		// build so builderd's cancel-LISTEN goroutine can call VM.Cancel
		// on each in-flight VM. Fire-and-forget — the row flip already
		// succeeded; the VM kill is best-effort (ReaperLoop is the safety net).
		ctx := r.Context()
		for _, buildID := range cancelledBuilds {
			payload := buildChangedPayload{BuildID: buildID, DeploymentID: id, Status: "cancelled", Reason: string(reason), Cascade: true}
			raw, _ := json.Marshal(payload)
			if err := s.notif.Notify(ctx, db.NotifyBuildChanged, string(raw)); err != nil {
				slog.Warn("apid: cancel build_changed notify", "build", buildID, "err", err)
			}
		}
		s.ops.ObserveDeploymentCancelled("ok")
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               d.ID,
			"status":           d.Status,
			"cancelled_at":     d.CancelledAt,
			"cancel_reason":    d.CancelReason,
			"cancelled_builds": cancelledBuilds,
		})
	}
}

// handleReorderDeployment — POST /v1/deployments/{id}/reorder.
//
// Body: {"priority": <int 0..1000>}. Plan-gated via
// Plan.QueueControlsAllowed() (Free=false; Hobby/Pro/Scale=true).
// 0 = "deploy immediately" (the UI's ↑ button); 100 = the FIFO
// default; 1000 = background rebuild. Refuses non-DeployPending
// rows with 409 so a reorder can't race against the builderd
// claim path.
func (s *server) handleReorderDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, _, ok := s.resolveDeploymentAccount(w, r, acct, id)
	if !ok {
		return
	}
	if !acct.Plan.QueueControlsAllowed() {
		s.ops.ObserveDeploymentReorder("plan_disabled")
		api.WriteProblem(w, api.ErrPlanReorderDisabled(acct.Plan))
		return
	}
	var req struct {
		Priority *int `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Priority == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"priority required",
			"body must be {\"priority\": <int in [0,1000]>}"))
		return
	}
	if err := s.store.ReorderDeployment(r.Context(), d.ID, *req.Priority, acct.ID); err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such deployment")
		case errors.Is(err, state.ErrReorderNotPending):
			s.ops.ObserveDeploymentReorder("not_pending")
			api.WriteProblem(w, api.ErrDeploymentReorderNotPending(id))
		case errors.Is(err, state.ErrPriorityOutOfRange):
			s.ops.ObserveDeploymentReorder("out_of_range")
			api.WriteProblem(w, api.ErrDeploymentReorderPriorityInvalid(*req.Priority))
		default:
			s.ops.ObserveDeploymentReorder("error")
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "reorder failed", err.Error()))
		}
		return
	}
	s.ops.ObserveDeploymentReorder("ok")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "priority": *req.Priority})
}

// handleClearDeployment — DELETE /v1/deployments/{id}.
//
// Soft-delete; status intentionally untouched (admin audit trail).
// 200 → {"id", "deleted_at"}. Free-allowed. Live deployments
// return 409: clearing a live row would orphan INV 3 / INV 4
// invariants. Customers should use `gregale deploys rollback`
// for that.
func (s *server) handleClearDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	if _, _, ok := s.resolveDeploymentAccount(w, r, acct, id); !ok {
		return
	}
	if err := s.store.ClearDeployment(r.Context(), id, acct.ID); err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such deployment")
		case errors.Is(err, state.ErrCancelLiveForbidden):
			api.WriteProblem(w, api.ErrDeploymentCancelLiveForbidden(id))
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "clear failed", err.Error()))
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "deleted_at": time.Now().UTC()})
}

// handleClearObsoleteDeployments — POST /v1/apps/{slug}/deployments/clear-obsolete.
//
// App-scoped bulk soft-delete of terminal-but-not-current
// deployments (status ∈ {superseded, failed, cancelled}). The
// "current + previous" retention window is enforced inside the
// store (ClearObsoleteDeployments) — INV 3 stays satisfied.
// Plan-gated via QueueControlsAllowed.
//
// Body: {"older_than"?: "168h"} — the app comes from the URL
// slug, not the body (the slug path-resolves the app which
// carries the IDOR gate). The body only carries the
// optional older-than cutoff (default 168h = 7d, matching the
// imaged nightly GC cycle).
func (s *server) handleClearObsoleteDeployments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.QueueControlsAllowed() {
		api.WriteProblem(w, api.ErrPlanReorderDisabled(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req struct {
		OlderThan string `json:"older_than"` // duration, e.g. "168h"
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body optional
	olderThan := 168 * time.Hour
	if req.OlderThan != "" {
		if d, err := time.ParseDuration(req.OlderThan); err == nil {
			olderThan = d
		}
	}
	count, err := s.store.ClearObsoleteDeployments(r.Context(), app.ID, time.Now().UTC().Add(-olderThan))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "clear-obsolete failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app_slug": app.Slug, "count": count, "older_than": olderThan.String()})
}
