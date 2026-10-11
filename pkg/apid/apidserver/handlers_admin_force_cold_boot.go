// handlers_admin_force_cold_boot.go — operator-side recovery primitive
// P2b (force-cold-boot-next-wake). PR #1099 P2 redesign: same
// pattern as force-park — INSERT operator_intents row +
// pg_notify + 202 Accepted. schedd's operator_intent_subscriber
// calls Engine.ForceColdBootNextWake which marks the warm +
// init snapshots stale (the recovery action per ADR-005:
// "snapshot of a wedged VM is a wedged VM"). No instance row
// mutation; the next customer Wake picks `haveSnap=false` and
// takes the cold-boot path.
//
// Auth + IDOR posture mirrors postForcePark: admin scope + MFA +
// s.adminAllows (allowlist check), ?confirm=true tripwire. The
// handler resolves the slug → apps row → latest deployments row
// (by created_at DESC) and forwards the deployment ID via the
// intent row's target_id field.
//
// The handler does NOT wait for the dispatch — the actual snap
// walk happens asynchronously in schedd, and the GET
// /v1/admin/operator-intents/{id} endpoint surfaces the
// snap_ids_marked_stale field on terminal status. Empty
// snap_ids means "deployment had no snapshots" (legitimate
// no-op) — the operator learns via the GET endpoint, not via
// the 202 response.
package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// forceColdBootReasonShape is the same a-z0-9_ shape used for
// force-park. We accept the same reason vocabulary for both
// recovery primitives so the operator dashboard's filter chip
// strip is consistent. Reason is optional (defaults to
// "operator_force_cold_boot").
var forceColdBootReasonShape = regexp.MustCompile(`^[a-z0-9_]*$`)

// maxForceColdBootReasonLen mirrors maxForceParkReasonLen so
// the audit log row's data JSON stays bounded across both
// recovery primitives.
const maxForceColdBootReasonLen = 64

// latestDeploymentForApp returns the most-recent deployment row
// for an app, via Store.LatestDeployment (created_at DESC).
// Single-deployment choice documented in plan §P2b edge cases.
// The Store method's contract is "the app's latest deployment
// row ordered DESC by created_at" (mirrors
// pkg/state/pgstore.go:4435), and returns state.ErrNotFound
// when the app has no deployments.
func (s *server) latestDeploymentForApp(ctx context.Context, appID string) (state.Deployment, error) {
	return s.store.LatestDeployment(ctx, appID)
}

// postForceColdBoot handles POST /v1/admin/apps/{slug}/force-cold-boot.
// 202 on success (intent row written), 400 on missing
// ?confirm=true or invalid ?reason=, 403 admin_required, 404
// app_not_found or deployment_not_found.
//
// Note: no 409 — the cold-boot path has no "not-eligible" state
// to gate on (the engine walks the tiers idempotently; an
// already-stale deployment is a no-op success, not a rejection).
func (s *server) postForceColdBoot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"confirm required",
			"?confirm=true is required to force-cold-boot; aborts on operator typo"))
		return
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "operator_force_cold_boot"
	}
	if len(reason) > maxForceColdBootReasonLen || !forceColdBootReasonShape.MatchString(reason) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid reason",
			"reason must match [a-z0-9_]{1,64}"))
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid slug", "slug path segment is empty"))
		return
	}

	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"app not found", "no app with that slug"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "app read failed", err.Error()))
		return
	}

	dep, err := s.latestDeploymentForApp(r.Context(), app.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"deployment not found", "app has no deployments to force-cold-boot"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "deployment read failed", err.Error()))
		return
	}

	var targetAccountPtr *string
	if app.AccountID != "" {
		acctID := app.AccountID
		targetAccountPtr = &acctID
	}

	// Insert the intent row. Source of truth — once durable, the
	// request returns 202.
	intentID, err := s.store.InsertOperatorIntent(
		r.Context(),
		state.OperatorIntentKindForceColdBoot,
		dep.ID,
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

	// Fire-and-forget notify (same precedent as force-park).
	notifyPayload, _ := json.Marshal(map[string]any{
		"intent_id": intentID,
		"kind":      string(state.OperatorIntentKindForceColdBoot),
		"target_id": dep.ID,
	})
	if nerr := s.notif.Notify(r.Context(), db.NotifyOperatorIntent, string(notifyPayload)); nerr != nil {
		s.log.Warn("apid: operator_intent: notify failed",
			"intent_id", intentID, "err", nerr)
	}

	// Emit the request-kind audit row with result="enqueued"
	// + intent_id. SnapIDs are NOT populated here — they're
	// stamped on the terminal outcome row emitted by schedd.
	emitOperatorActionForceColdBoot(r, s, acct, app.AccountID,
		app.ID, dep.ID, "enqueued", intentID, nil)

	writeJSON(w, http.StatusAccepted, api.OperatorIntentAcceptedResponse{
		OK:           true,
		IntentID:     intentID,
		StatusURL:    "/v1/admin/operator-intents/" + intentID,
		ExpiresAt:    time.Now().UTC().Add(operatorIntentPollHorizon),
		Kind:         string(state.OperatorIntentKindForceColdBoot),
		AppID:        app.ID,
		DeploymentID: dep.ID,
		Reason:       reason,
	})
}
