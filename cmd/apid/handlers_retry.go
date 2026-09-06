// Per-stage retry endpoint. ADR-117 §Production-ready follow-on,
// C2.
//
// Wire shape:
//
//	POST /v1/apps/{slug}/deployments/{id}/retry
//	Body: {"from_stage": "<StageName>"}
//	202 Accepted
//	{
//	  "id": "<new-deployment-id>",
//	  "app_id": "...",
//	  "status": "pending",
//	  "stage_state": {
//	    "current": "<StageName>",
//	    "current_started_at": null,
//	    "history": []
//	  },
//	  ...
//	}
//
// 400 if from_stage is empty or not one of the closed-6 vocabulary.
// 404 if the deployment does not exist OR is in another account
// (IDOR posture: never reveal cross-account existence; matches
// getDeployment / getDeploymentStages / getDeploymentScan).
// 500 for storage-layer failures.
//
// The handler is non-idempotent (NOT wrapped in s.idempotent).
// Every retry call creates a fresh deployments row. The CLI usage
// text calls out that `from_stage = source_download` re-runs the
// whole pipeline — the retry-from-top case is intentional.
//
// Auth chain mirrors POST /v1/apps/{slug}/deployments
// (authLimited → requireMFA → requireScope(ScopesDeployWriteSurface)).
// Returns the new row through the standard deploymentResponse
// helper so the CLI's `gregale deploys retry <id>` can render the
// summary line via the same path as a fresh deploy.
package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) retryDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	// 1. Load the failed row. 404 (not 403) on cross-account; never
	//    reveal whether the deployment_id exists in another
	//    account. Same posture as getDeploymentStages.
	dep, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such deployment")
			return
		}
		api.WriteProblem(w, api.ErrInternal(
			fmt.Sprintf("load deployment: %v", err)))
		return
	}
	// 2. Resolve the parent app + cross-account guard. The route
	//    is keyed on {id} (no slug), so the cross-account check
	//    goes through AppByID + AccountID. This is the same
	//    IDOR posture as getDeploymentStages (404 not 403; never
	//    reveal cross-account existence).
	app, err := s.store.AppByID(r.Context(), dep.AppID)
	if err != nil {
		// Real DB failure (timeout, conn lost, etc.) — surface
		// as 500 so the operator can distinguish from a missing
		// row. Collapsing into the IDOR 404 path would mask
		// outages as "no such deployment".
		api.WriteProblem(w, api.ErrInternal(
			fmt.Sprintf("load app: %v", err)))
		return
	}
	if app.AccountID != acct.ID {
		// Cross-account probe. 404, not 403.
		s.notFound(w, "no such deployment")
		return
	}

	// 3. Parse the request body. Body is small (≤ 1 KB); the
	//    decodeJSON helper applies MaxBytesReader + DisallowUnknownFields
	//    so a typo'd field trips 400 rather than silently binding.
	var req api.RetryDeploymentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	// 4. Closed-vocab guard. Unknown from_stage → 400 with a
	//    structured RFC 7807 problem. The storage layer ALSO
	//    validates (defence in depth) — a future caller that
	//    bypasses this handler gets ErrInvalidArgument, not a
	//    silent row write.
	if !state.IsStageName(state.StageName(req.FromStage)) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid from_stage",
			fmt.Sprintf("from_stage must be one of the closed-6 stage vocabulary: %v", state.AllStageNames)))
		return
	}

	// 5. Insert the fresh row. PgStore/memstore handle the
	//    vocabulary re-check + the input-primitive copy; the
	//    handler is the seam that decides "what the wire looks
	//    like" and "what the auth posture is". Storage returns
	//    ErrInvalidArgument for a closed-vocab slip; we map that
	//    to 400 even though the storage-layer call already passed
	//    the IsStageName check — belt-and-suspenders.
	newDep, err := s.enqueueRetry(r.Context(), app, dep, state.StageName(req.FromStage))
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid from_stage",
				"from_stage is not in the closed-6 stage vocabulary"))
			return
		}
		s.log.Warn("retry deployment enqueue failed", "deployment", id, "err", err)
		api.WriteProblem(w, api.ErrInternal("Could not enqueue the deployment retry."))
		return
	}
	// Source retries have a durable build; image retries have notified imaged.
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(newDep, app))
}
