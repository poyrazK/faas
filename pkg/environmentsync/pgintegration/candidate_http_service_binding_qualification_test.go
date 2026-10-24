// adr: 20261019200059 — plain HTTP service edges qualify enforced candidates.
package pgintegration_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCandidateBindingHTTPServiceEdgesQualify(t *testing.T) {
	for _, exchange := range []bool{false, true} {
		t.Run(map[bool]string{false: "without exchange", true: "with exchange"}[exchange], func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, plan, requests, placement := candidateBindingFixtureFor(t, basic, api.ServiceBindingTransportHTTP, "", "", nil)
				store := basic.(candidateBindingTestStore)
				// The helper also proves a receipt claiming the other transport is refused.
				completeCandidateBindingCohort(t, store, requests, placement, exchange)
				release, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
				if !exchange {
					if active || !state.IsBindingReleaseRequired(err) {
						t.Fatalf("HTTP edge without an exchange activated: %v %v", active, err)
					}
					assertCandidatesHeld(t, basic, requests)
					return
				}
				if err != nil || !active || len(release.Members) != 2 {
					t.Fatalf("checked activation over HTTP edges: %+v %v %v", release, active, err)
				}
			})
		})
	}
}

func TestPgCallerTransportMirrorsEffectiveManifest(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ frozen, want string }{
		{`{}`, "http"},
		{`{"baseline":{"service_binding_transport":"https"}}`, "https"},
		{`{"baseline":{"service_binding_transport":"https"},"runtime":{"service_binding_transport":"http"}}`, "http"},
		// A runtime value replaces the baseline one even when empty.
		{`{"baseline":{"service_binding_transport":"https"},"runtime":{"service_binding_transport":""}}`, "http"},
		{`{"baseline":{"service_binding_transport":"http"},"runtime":{"service_binding_transport":"https"}}`, "https"},
		{`{"runtime":{"service_binding_transport":"future"}}`, "https"},
	} {
		var got string
		if err := pool.QueryRow(t.Context(), `SELECT environment_qualification_caller_transport($1::jsonb)`, tc.frozen).Scan(&got); err != nil || got != tc.want {
			t.Fatalf("%s: %q %v", tc.frozen, got, err)
		}
	}
}
