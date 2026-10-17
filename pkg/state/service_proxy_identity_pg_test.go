//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgLiveServiceIdentityGroupsOwnersBeforeLimit(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	_, firstApp, firstDeployment := seedLiveDeploy(t, store, ctx, "identity-group-first", "identity-group-first")
	_, secondApp, secondDeployment := seedLiveDeploy(t, store, ctx, "identity-group-second", "identity-group-second")
	// With sorted DISTINCT, both deployments of the first UUID precede the
	// other owner. Limiting deployment identities to two would hide that owner.
	if firstApp > secondApp {
		firstApp, secondApp = secondApp, firstApp
		firstDeployment, secondDeployment = secondDeployment, firstDeployment
	}
	extraDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: firstApp, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:identity-group-extra", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, extraDeployment.ID); err != nil {
		t.Fatal(err)
	}
	node := resolveDefaultLocal(t, ctx, store)
	var instances []state.Instance
	for index, owner := range []struct{ app, deployment string }{
		{firstApp, firstDeployment}, {firstApp, extraDeployment.ID}, {secondApp, secondDeployment},
	} {
		instance, err := store.CreateInstance(ctx, owner.app, owner.deployment, string(state.StateRunning), 128, node, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetInstanceRuntime(ctx, instance.ID, "fixture-"+instance.ID, "10.100.0.5", 23000+index); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	for _, aggregate := range []string{"on", "off"} {
		t.Run("hash_aggregate_"+aggregate, func(t *testing.T) {
			config := pool.Config()
			config.ConnConfig.RuntimeParams["enable_hashagg"] = aggregate
			readerPool, err := pgxpool.NewWithConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(readerPool.Close)
			rows, err := state.NewPgStore(readerPool).LiveInstancesByHostIP(t.Context(), state.DefaultLocalNodeName, "10.100.0.5")
			if err != nil || len(rows) != 2 || rows[0].AppID == rows[1].AppID {
				t.Fatalf("two deployments hid the other live owner: rows=%+v err=%v", rows, err)
			}
		})
	}
	if err := store.UpdateInstanceState(ctx, instances[2].ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	rows, err := store.LiveInstancesByHostIP(ctx, state.DefaultLocalNodeName, "10.100.0.5")
	if err != nil || len(rows) != 1 || rows[0].AppID != firstApp || rows[0].DeploymentID != "" {
		t.Fatalf("same-app overlap must retain app but clear deployment identity: rows=%+v err=%v", rows, err)
	}
	if err := store.UpdateInstanceState(ctx, instances[1].ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	rows, err = store.LiveInstancesByHostIP(ctx, state.DefaultLocalNodeName, "10.100.0.5")
	if err != nil || len(rows) != 1 || rows[0].AppID != firstApp || rows[0].DeploymentID != firstDeployment {
		t.Fatalf("unique deployment identity did not recover: rows=%+v err=%v", rows, err)
	}
}
