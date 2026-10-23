// adr: 20261022194510
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cronNotFound() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such cron")
}

// cronRunRoute captures POST /v1/crons/{id}/run. A guest names a cron of its
// account, usually a production one, so the request is captured and never
// fired; it stays pending.
func cronRunRoute(id string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		if _, err := uuid.Parse(id); err != nil {
			api.WriteProblem(w, api.ErrCronInvalid("invalid cron id (want UUID)"))
			return
		}
		limits, ok := api.LimitsFor(call.policy.Plan)
		if !ok || limits.CronLimitPerApp == 0 {
			api.WriteProblem(w, api.ErrPlanCronsNotAllowed(call.policy.Plan))
			return
		}
		capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindCronRun, Address: id, Name: "run", IdempotencyKey: rehearsalExecutionKey(r)}
		capture.Message, capture.Oversized = rehearsalPlatformMessage(map[string]json.RawMessage{}, 0)
		result, problem := captureRehearsalPlatformCall(r, call, capture, cronNotFound(), nil)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if result.Replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		writeRehearsalCaptured(w, http.StatusAccepted, api.FireCronResponse{RequestID: result.ID, CronID: id, Status: "pending"})
	}
}

// cronRequestReadRoute serves GET /v1/cron-fire-now-requests/{request_id} for
// the run's own captured fire-now: pending, since it never fires.
func cronRequestReadRoute(id string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		record, err := call.store.ScenarioCapturedPlatformCall(r.Context(), call.policy.AccountID, call.policy.RunID, id)
		switch {
		case errors.Is(err, state.ErrNotFound) || err == nil && record.Kind != api.ScenarioPlatformKindCronRun:
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such fire-now request"))
			return
		case err != nil:
			api.WriteProblem(w, api.ErrCapacity("rehearsal capture unavailable"))
			return
		}
		writeRehearsalCaptured(w, http.StatusOK, api.FireCronRequestResponse{RequestID: id, CronID: strings.TrimPrefix(record.Target, "cron:"), Status: "pending",
			RequestedAt: record.CreatedAt.UTC().Format(time.RFC3339Nano), AccountID: call.policy.AccountID})
	}
}

// jobsUnavailableRoute refuses job runs. A Job runs its own image in a job VM
// with no deployment, outside the containment boundary, so it can be neither
// pinned to the run nor performed.
func jobsUnavailableRoute(w http.ResponseWriter, _ *http.Request, _ rehearsalPlatformCall) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Jobs unavailable in a rehearsal",
		"a job runs its own image outside the rehearsal's containment; run its work as a contained command from the recipe").
		WithDocs("https://gregale.dev/docs/scenario-tests#contain-serving-vm-outbound-traffic"))
}
