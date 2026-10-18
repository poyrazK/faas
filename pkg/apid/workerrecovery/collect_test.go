// adr: 20261017223000
package workerrecovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/workerrecovery"
	"github.com/onebox-faas/faas/pkg/state"
)

type reader struct {
	row state.WorkerRecovery
	err error
}

func (r reader) WorkerRecoveryByApp(context.Context, string) (state.WorkerRecovery, error) {
	return r.row, r.err
}

func TestCollectPreservesAppRetryAndFiltersEnvironmentObservations(t *testing.T) {
	now := time.Now().UTC()
	app := state.App{ID: "app", Status: state.AppActive}
	account := state.Account{Status: state.AccountActive}
	r := reader{row: state.WorkerRecovery{AppID: app.ID, Revision: "PRIVATE_REVISION", ClaimToken: "PRIVATE_TOKEN",
		Status: "waiting_capacity", Failures: 3, UpdatedAt: now, NextAttemptAt: now.Add(20 * time.Second), LeaseUntil: now.Add(time.Minute),
		Pools: []state.WorkerRecoveryPool{
			{Scope: "default", DeploymentID: "production", Revision: "PRIVATE_REVISION", Desired: 2, ObservedAt: now},
			{Scope: "staging", DeploymentID: "stage", Revision: "PRIVATE_REVISION", Desired: 1, Running: 1, ObservedAt: now},
			{Scope: "old", Revision: "obsolete", Desired: 5, ObservedAt: now},
		}}}
	got := workerrecovery.Collect(t.Context(), r, app, account, "staging", now)
	if got == nil || !got.Available || !got.Eligible || got.Scope != "app" || got.Status != "waiting_capacity" || got.Failures != 3 ||
		got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(r.row.NextAttemptAt) || got.LeaseUntil == nil {
		t.Fatalf("app retry evidence changed: %+v", got)
	}
	if len(got.Pools) != 1 || got.Pools[0].Scope != "staging" || got.Pools[0].Running != 1 || got.Pools[0].Stale {
		t.Fatalf("environment evidence crossed scope: %+v", got.Pools)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE_TOKEN", "PRIVATE_REVISION", "production", "obsolete"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private or neighboring evidence exposed: %s", encoded)
		}
	}
	all := workerrecovery.Collect(t.Context(), r, app, account, "", now)
	if len(all.Pools) != 2 {
		t.Fatalf("unscoped summary lost current environments: %+v", all)
	}
}

func TestCollectUnknownStaleAndInactiveEvidence(t *testing.T) {
	now := time.Now().UTC()
	worker := state.App{ID: "worker", Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}}
	active := state.Account{Status: state.AccountActive}
	for _, tc := range []struct {
		name, status string
		err          error
		available    bool
	}{
		{"no attempt", "not_observed", state.ErrNotFound, true},
		{"read failed", "unknown", errors.New("PRIVATE_DATABASE_ERROR"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := workerrecovery.Collect(t.Context(), reader{err: tc.err}, worker, active, "", now)
			if got == nil || got.Status != tc.status || got.Available != tc.available || len(got.Pools) != 0 || got.NextAttemptAt != nil {
				t.Fatalf("missing evidence became healthy counts: %+v", got)
			}
			if strings.Contains(got.Reason, "PRIVATE") {
				t.Fatalf("raw error exposed: %+v", got)
			}
		})
	}
	request := worker
	request.Manifest.ExecutionMode = api.ExecutionModeRequest
	if got := workerrecovery.Collect(t.Context(), reader{err: state.ErrNotFound}, request, active, "", now); got != nil {
		t.Fatalf("request-only app acquired worker evidence: %+v", got)
	}
	failedCaptured := reader{row: state.WorkerRecovery{Status: "waiting_dependency", Failures: 1, UpdatedAt: now}}
	if got := workerrecovery.Collect(t.Context(), failedCaptured, request, active, "", now); got == nil || got.Status != "waiting_dependency" || len(got.Pools) != 0 {
		t.Fatalf("request app head hid an unobserved captured worker failure: %+v", got)
	}
	if got := workerrecovery.Collect(t.Context(), reader{row: state.WorkerRecovery{Status: "ready"}}, request, active, "", now); got != nil {
		t.Fatalf("retired request-only recovery was not omitted: %+v", got)
	}
	r := reader{row: state.WorkerRecovery{Revision: "r", Status: "ready", UpdatedAt: now,
		Pools: []state.WorkerRecoveryPool{{Scope: "default", Revision: "r", Desired: 0, ObservedAt: now.Add(-time.Duration(api.WorkerRecoveryObservationFreshnessSeconds+1) * time.Second)}}}}
	worker.Status = state.AppEvictedCold
	got := workerrecovery.Collect(t.Context(), r, worker, active, "", now)
	if got.Eligible || len(got.Pools) != 1 || !got.Pools[0].Stale || got.LeaseUntil != nil {
		t.Fatalf("stale/parked evidence misrepresented: %+v", got)
	}
	worker.Status = state.AppActive
	active.Status = state.AccountSuspended
	if workerrecovery.Collect(t.Context(), r, worker, active, "", now).Eligible {
		t.Fatal("suspended account reported eligible")
	}
}
