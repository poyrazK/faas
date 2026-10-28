// Per-deployment closed-stage read surface. Companion to GET
// /v1/deployments/{id}/logs (which streams the SSE event: stage
// frames during a live deploy) and GET /v1/deployments/{id} (which
// returns the typed state.Deployment row). This endpoint serves the
// post-stream summary use case — `gregale deploys show <id>` and
// the future dashboard widget.
//
// Wire shape:
//
//	GET /v1/deployments/{id}/stages
//	200 OK
//	{
//	  "current": "<StageName>",
//	  "current_started_at": "<RFC3339Nano>" | null,
//	  "history": [
//	    {"name":"<StageName>","started_at":"<RFC3339Nano>",
//	     "ended_at":"<RFC3339Nano>","duration_ms":<int64>,
//	     "status":"completed"|"failed","reason":"<string>"|""},
//	    ...
//	  ]
//	}
//
// The body is the same `state.StageState` JSON shape already stored on
// `deployments.stage_state` (ADR-117, migration 00302). The handler
// does NOT add a typed API DTO — the column's jsonb is the wire. The
// closed-vocabulary CHECK (`deployments_stage_state_current_check`)
// guarantees `current` is one of the 6 customer-visible stages; the
// typed StageName const on pkg/state is the Go-side mirror.
//
// IDOR posture mirrors getDeployment / getDeploymentScan: 404 on
// cross-account, never 403 (we don't reveal deployment existence
// across accounts). 404 also covers the pre-ADR-117 row where the
// column was added at slot 00302 — a freshly-deployed row will
// already have stage_state, but a row from before the migration
// would not. For those, the migration backfilled the column default
// (see migrations/00302_deployments_stage_state.sql), so the only
// 404 condition the handler ever emits is "not your deployment".
package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getDeploymentStages(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such deployment")
			return
		}
		logCustomerFailure(s.log, "load deployment", err)
		api.WriteProblem(w, api.ErrInternal(
			api.DetailServerFailure))
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil {
		// Real DB failure (timeout, conn lost, etc.) — surface
		// as 500 so the operator can distinguish from a missing
		// row. Collapsing this into the IDOR 404 path would
		// mask outages as "no such deployment".
		logCustomerFailure(s.log, "load app", err)
		api.WriteProblem(w, api.ErrInternal(
			api.DetailServerFailure))
		return
	}
	if app.AccountID != acct.ID {
		// Cross-account probes get 404, not 403 — never reveal
		// whether the deployment_id exists in another account.
		// Same posture as getDeployment / getDeploymentScan.
		s.notFound(w, "no such deployment")
		return
	}

	// The column carries a jsonb default for every row (the
	// migration set NOT NULL DEFAULT '{...}'); a missing column
	// would mean a deployment that pre-dates the migration AND
	// somehow escaped the backfill, which the embed_test replay
	// test pins as unreachable. The `null` branch is therefore
	// unreachable in practice, but we render it as an empty
	// summary rather than a 404 so a future schema change (e.g.
	// dropping the column) doesn't break the endpoint.
	if len(d.StageState) == 0 {
		writeJSON(w, http.StatusOK, &state.StageState{
			Current: state.StageSourceDownload,
			History: []state.StageStateItem{},
		})
		return
	}
	// Re-emit the raw jsonb bytes — the column IS the wire. This
	// avoids a Go-side round-trip json.Unmarshal → json.Marshal
	// that would silently rename fields if the typed struct ever
	// drifts from the jsonb shape. A 500 here would mean the
	// jsonb is corrupt; the migration's CHECK guard plus the
	// TestMigrations_00302_DeploymentsStageState pin both keep
	// that from happening.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(d.StageState)
}
