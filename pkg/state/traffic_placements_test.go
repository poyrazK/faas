// adr: 531
package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTrafficPlacementsBoundsBeforeDatabase(t *testing.T) {
	store := &state.PgStore{}
	for _, tc := range []struct {
		name string
		apps []string
		bad  bool
	}{
		{"empty", nil, false}, {"overflow", make([]string, api.TrafficPlacementAppBatchSize+1), true},
		{"empty identity", []string{""}, true}, {"invalid identity", []string{"not-a-uuid"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.RunningTrafficPlacements(t.Context(), tc.apps)
			if (err != nil) != tc.bad || len(got) != 0 {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}

func TestTrafficPlacementsPostgresCompletenessHistoryAndOwners(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "traffic-placement-state@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "placement-state", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:placement", Scope: "production", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	node := resolveDefaultLocal(t, ctx, store)
	if _, err := pool.Exec(ctx, `UPDATE deployments SET inferred_profile='{"version":1,"port":9090,"private_extra":"must-not-project"}', override_port=8081 WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	insert := func(count int, phase string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO instances(id,app_id,deployment_id,node_id,wake_id,state,ram_mb) SELECT gen_random_uuid(),$1,$2,$3,gen_random_uuid(),$4,128 FROM generate_series(1,$5::int)`, app.ID, dep.ID, node, phase, count); err != nil {
			t.Fatal(err)
		}
	}
	insert(2000, string(state.StateParked))
	missing := uuid.NewString()
	read := func() map[string]state.TrafficPlacementSnapshot {
		t.Helper()
		got, err := store.RunningTrafficPlacements(ctx, []string{app.ID, missing, app.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || !got[missing].Complete || len(got[missing].Targets) != 0 {
			t.Fatalf("explicit empty/duplicate result: %+v", got)
		}
		return got
	}
	if got := read()[app.ID]; !got.Complete || len(got.Targets) != 0 {
		t.Fatal("terminal history entered snapshot")
	}
	insert(api.TrafficPlacementTargetsPerApp, string(state.StateRunning))
	got := read()[app.ID]
	if !got.Complete || len(got.Targets) != api.TrafficPlacementTargetsPerApp {
		t.Fatalf("exact bound: %+v", got)
	}
	for _, target := range got.Targets {
		if target.AppID != app.ID || target.DeploymentID != dep.ID || target.NodeID != node || target.WakeID == "" || !target.DeploymentLive || target.OverridePort != 8081 || strings.Contains(string(target.InferredProfile), "must-not-project") {
			t.Fatalf("wrong projection: %+v", target)
		}
	}
	insert(1, string(state.StateRunning))
	got = read()[app.ID]
	if got.Complete || len(got.Targets) != api.TrafficPlacementTargetsPerApp {
		t.Fatal("overflow falsely certified absence")
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET status='superseded' WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	got = read()[app.ID]
	if got.Targets[0].DeploymentLive {
		t.Fatal("retired cohort discovered as live")
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET deleted_at=now() WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	if got := read()[app.ID]; !got.Complete || len(got.Targets) != 0 {
		t.Fatal("deleted deployment retained placements")
	}
}
