// adr: 20261017223000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkerRecoveryOperationalSummaryHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, account := *e.Store(), *e.Acct()
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "recovery"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "worker-diagnostics", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r, claimed, err := store.ClaimWorkerRecovery(t.Context(), app.ID, "", "r", uuid.NewString(), now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	r.Status, r.Failures, r.NextAttemptAt = "waiting_capacity", 2, now.Add(10*time.Second)
	r.Pools = []state.WorkerRecoveryPool{
		{Scope: "default", Revision: "r", DeploymentID: uuid.NewString(), ObservedAt: now, Desired: 2, Running: 1},
		{Scope: "staging", Revision: "r", DeploymentID: uuid.NewString(), ObservedAt: now, Desired: 1, Running: 1},
	}
	if err := store.CompleteWorkerRecovery(t.Context(), r.ClaimToken, r); err != nil {
		t.Fatal(err)
	}
	before, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, scope string
		pools        int
	}{
		{"", "", 2},
		{"?scope=default", "default", 1},
		{"?environment=staging", "staging", 1},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rec := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/operational-summary"+tc.query, nil, nil)
			var got api.AppOperationalSummary
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
				t.Fatalf("summary: %d %s", rec.Code, rec.Body)
			}
			worker := got.Recovery.Workers
			if worker == nil || !worker.Available || worker.Status != "waiting_capacity" || worker.Scope != "app" || worker.Failures != 2 ||
				worker.NextAttemptAt == nil || !worker.NextAttemptAt.Equal(r.NextAttemptAt) || len(worker.Pools) != tc.pools {
				t.Fatalf("app recovery misrepresented: %+v", worker)
			}
			if tc.scope != "" && worker.Pools[0].Scope != tc.scope {
				t.Fatalf("neighboring worker counts disclosed: %+v", worker.Pools)
			}
		})
	}
	after, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("summary mutated reconciliation state: %+v %v", after, err)
	}
	if rows, err := store.ListInstancesForApp(t.Context(), app.ID); err != nil || len(rows) != 0 {
		t.Fatalf("summary started workers: %+v %v", rows, err)
	}
}
