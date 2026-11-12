package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261104141122 step 4: a passing canary compares the binding's pools
// across the app's instance ceiling with the database's qualified allocation.
func TestCmdBindingsVerifyPostgresComparesConnectionsWithTheAllocation(t *testing.T) {
	databaseID := uuid.NewString()
	budget := func(usable int64) api.ManagedPostgresConnectionBudget {
		now := time.Now().UTC()
		return api.ManagedPostgresConnectionBudget{
			Version: 1, DatabaseID: databaseID, DatabaseName: "primary", State: "within_budget",
			CheckedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(api.DataAPIConnectionBudgetMaxAge).Format(time.RFC3339Nano),
			Model: "canonical_runtime_pools", Reasons: []string{},
			Demand:   api.ConnectionBudgetDemand{DataAPIResidentEstimate: 2, DataAPIWakeEstimate: 6, ControlHeadroom: 1, ModeledPeakConnections: 7},
			Capacity: api.ConnectionBudgetCapacity{State: "verified", UsableConnections: &usable},
		}
	}
	for _, tc := range []struct {
		name        string
		poolMax     int
		budget      *api.ManagedPostgresConnectionBudget
		status      string
		total       int
		detail      string
		budgetReads int
	}{
		{name: "fits the allocation", poolMax: 5, budget: postgresBudgetPtr(budget(40)), status: "passed", total: 20, detail: "fits in the 40", budgetReads: 1},
		{name: "over the allocation", poolMax: 5, budget: postgresBudgetPtr(budget(12)), status: "warning", total: 20, detail: "more than the 12", budgetReads: 1},
		{name: "budget unreadable", poolMax: 5, status: "not_checked", total: 20, detail: "could not be read", budgetReads: 1},
		{name: "pool size unset", poolMax: 0, budget: postgresBudgetPtr(budget(12)), status: "not_checked", detail: api.PostgresPoolMaxEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reportJSON, err := json.Marshal(api.PostgresBindingProbeReport{
				EnvironmentKey: "DATABASE_URL", PoolMax: tc.poolMax,
				Environment: api.PostgresBindingProbeCheck{Status: "passed"}, Configuration: api.PostgresBindingProbeCheck{Status: "passed"},
				Connection: api.PostgresBindingProbeCheck{Status: "passed"}, Query: api.PostgresBindingProbeCheck{Status: "passed"},
				// A guest cannot supply the comparison itself.
				Connections: &api.PostgresBindingConnections{Status: "passed", Detail: "from the guest"},
			})
			if err != nil {
				t.Fatal(err)
			}
			budgetReads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
					_, _ = w.Write([]byte(`{"id":"app-1","slug":"api","max_concurrency":3}`))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
					_, _ = w.Write([]byte(`{"app":"api","verification_scope":"default","verification_deployment_id":"deployment-1","bindings":[{"type":"postgres","name":"primary","binding":"DATABASE_URL","scope":"default"}]}`))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task-pg-1", AppID: "app-1", DeploymentID: "deployment-1",
						Kind: api.AppTaskKindManual, Status: api.AppTaskStatusSucceeded, StdoutTail: string(reportJSON)})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
					_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: databaseID, Name: "primary"}}})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/"+databaseID+"/connection-budget":
					budgetReads++
					if tc.budget == nil {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode(tc.budget)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			previousJSON := (*main.JsonOutput)
			(*main.JsonOutput) = true
			t.Cleanup(func() { (*main.JsonOutput) = previousJSON })
			var out bytes.Buffer
			previousOut := (*main.OsStdout)
			(*main.OsStdout) = &out
			t.Cleanup(func() { (*main.OsStdout) = previousOut })

			if code := main.Run([]string{"bindings", "verify", "api", "--postgres", "DATABASE_URL", "--poll-interval", "1ms", "--wait-timeout", "1s"}); code != 0 {
				t.Fatalf("exit = %d, want 0 (the comparison never fails verification); output = %s", code, out.String())
			}
			var got api.PostgresBindingProbeReport
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode report: %v; output=%s", err, out.String())
			}
			c := got.Connections
			if c == nil || c.Status != tc.status || c.Total != tc.total || !strings.Contains(c.Detail, tc.detail) {
				t.Fatalf("connections = %+v, want status %s total %d detail containing %q", c, tc.status, tc.total, tc.detail)
			}
			if budgetReads != tc.budgetReads {
				t.Fatalf("budget reads = %d, want %d", budgetReads, tc.budgetReads)
			}
		})
	}
}

func postgresBudgetPtr[T any](v T) *T { return &v }
