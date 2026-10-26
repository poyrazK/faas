// adr: 570 — a GitOps candidate's new revision URL joins the shared binding guard.
package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A legacy rule whose canonical JSON exceeds the runtime read cap already
// covers every existing URL; only a newly exposed revision URL is refused.
// Only PostgreSQL can hold such a row, so there is no memory variant here.
func TestPgGitOpsCandidateRevisionURLJoinsBindingGuard(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	lease, plan, _ := workloadGraphFixtureWithWorkerSources(t, store, true, api.ServiceBindingTransportHTTPS, nil, "")
	apps, err := store.ListApps(t.Context(), lease.Source.AccountID)
	if err != nil || len(apps) == 0 {
		t.Fatal("fixture apps", err)
	}
	action, err := json.Marshal(state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: apps[0].Slug},
		Validate: &state.EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 519) + "1e130000]")}})
	if err != nil {
		t.Fatal(err)
	}
	rule := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,'*','/',true,'route',$4::jsonb)`,
		rule, lease.Source.AccountID, apps[0].ID, action); err != nil {
		t.Fatal(err)
	}
	countDeployments := func() int {
		var n int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM deployments`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := countDeployments()
	_, err = store.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, nil)
	var aggregate *state.TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" || !strings.HasPrefix(aggregate.Host, "deploy-") {
		t.Fatalf("candidate published an oversized revision URL: %v", err)
	}
	if after := countDeployments(); after != before {
		t.Fatalf("refused preparation kept candidates: before=%d after=%d", before, after)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM edge_rules WHERE id=$1`, rule); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, nil)
	if err != nil || len(candidates) == 0 {
		t.Fatalf("preparation after repair: %+v %v", candidates, err)
	}
}
