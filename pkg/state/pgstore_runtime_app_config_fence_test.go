//go:build !no_pg

// adr: 583
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeInstancePublicationConfiguration(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeInstancePublicationConfiguration(t, store)
}

func TestPgLegacyRuntimeConfigFenceProjection(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testLegacyRuntimeConfigFenceProjection(t, store)
}

func TestPgRuntimePublicationRejectsCorruptPinnedSettings(t *testing.T) {
	for _, mutation := range []string{"unknown", "changed"} {
		t.Run(mutation, func(t *testing.T) {
			store, ctx, pool := pgWithPool(t)
			f := seedRuntimeAppEnv(t, store)
			p := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], runtimeSecretNodeForTest(t, store))
			patch := `{"unexpected":true}`
			if mutation == "changed" {
				patch = `{"ram_mb":512}`
			}
			if _, err := pool.Exec(ctx, `UPDATE project_environment_workload_specs SET settings=(settings::jsonb||$2::jsonb)::json
				WHERE id=(SELECT spec_id FROM project_environment_workload_deployment_specs WHERE deployment_id=$1)`, p.Fence.DeploymentID, patch); err != nil {
				t.Fatal(err)
			}
			if _, err := store.PublishOwnedInstanceRuntime(ctx, p); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("corrupt deployed settings published: %v", err)
			}
			assertRuntimePublicationUnchanged(t.Context(), t, store, p)
		})
	}
}

func TestPgPausedRuntimeConfigProofCannotAdoptNewValues(t *testing.T) {
	store, _, pool := pgWithPool(t)
	testPausedRuntimeConfigProofCannotAdoptNewValues(t, store)
	f := seedRuntimeAppEnv(t, store)
	node, err := store.ComputeNodeByName(t.Context(), "runtime-secret-fence")
	if err != nil {
		t.Fatal(err)
	}
	p := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], node.ID)
	if _, err := store.PublishOwnedInstanceRuntime(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	proof, err := state.NewPgStore(pool).InstanceRuntimeConfigFence(t.Context(), f.account.ID, f.app.ID, p.InstanceID)
	if err != nil || proof != p.ConfigFence {
		t.Fatalf("fresh Store lost committed proof: %v", err)
	}
}

func TestPgRuntimePublicationWaitsForConfigurationEdits(t *testing.T) {
	for _, mutation := range []string{"insert", "update", "sidecar-signal", "sidecar-layer"} {
		t.Run(mutation, func(t *testing.T) {
			store, ctx, pool := pgWithPool(t)
			f := seedRuntimeAppEnv(t, store)
			dep := f.deployments["stage"]
			if mutation == "insert" {
				if err := store.DeleteAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE"); err != nil {
					t.Fatal(err)
				}
			}
			p := seedRuntimeInstancePublication(t, store, f, dep, runtimeSecretNodeForTest(t, store))
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			var blocker int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			if mutation == "insert" {
				_, err = tx.Exec(ctx, `INSERT INTO app_envs(account_id,app_id,scope,key,value) VALUES($1,$2,'stage','NEW','changed')`, f.account.ID, f.app.ID)
			} else if mutation == "update" {
				_, err = tx.Exec(ctx, `UPDATE app_envs SET value='changed' WHERE app_id=$1 AND scope='stage' AND key='MODE'`, f.app.ID)
			} else if mutation == "sidecar-signal" {
				_, err = tx.Exec(ctx, `INSERT INTO deployment_sidecar_secret_reload_signals(deployment_id,sidecar_name,signal) VALUES($1,'helper','SIGHUP')`, dep.ID)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO deployment_sidecar_layers(deployment_id,sidecar_name,storage_key,bytes,content_digest) VALUES($1,'helper','apps/helper.ext4',4096,'sha256:helper')`, dep.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := store.PublishOwnedInstanceRuntime(ctx, p); done <- err }()
			deadline, tick := time.NewTimer(5*time.Second), time.NewTicker(10*time.Millisecond)
			defer deadline.Stop()
			defer tick.Stop()
			prefix := "-- name: LockRuntimeSecretApp%"
			if mutation == "sidecar-signal" || mutation == "sidecar-layer" {
				prefix = "-- name: LockRuntimeSecretOwner%"
			}
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE $2 AND $1=ANY(pg_blocking_pids(pid)))`, blocker, prefix).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("publication bypassed writer: %v", err)
				case <-deadline.C:
					t.Fatal("publication did not reach writer lock")
				case <-tick.C:
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, state.ErrConflict) {
					t.Fatalf("publication adopted edited variables: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("publication stuck after variable writer committed")
			}
			assertRuntimePublicationUnchanged(t.Context(), t, store, p)
		})
	}
}

func TestPgRuntimePublicationDoesNotWaitOnBlockedConfigTuple(t *testing.T) {
	for _, kind := range []string{"variable", "secret", "sidecar"} {
		t.Run(kind, func(t *testing.T) {
			store, ctx, pool := pgWithPool(t)
			f := seedRuntimeAppEnv(t, store)
			dep := f.deployments["stage"]
			if kind == "secret" {
				if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("original")); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "sidecar" {
				if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: dep.ID, SidecarName: "helper", StorageKey: "apps/original.ext4"}); err != nil {
					t.Fatal(err)
				}
			}
			p := seedRuntimeInstancePublication(t, store, f, dep, runtimeSecretNodeForTest(t, store))
			hold, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = hold.Rollback(ctx) }()
			var blocker int
			if err := hold.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			query, key, prefix := `SELECT id FROM apps WHERE id=$1 FOR SHARE`, f.app.ID, "UPDATE app_envs%"
			if kind == "sidecar" {
				query, key, prefix = `SELECT id FROM deployments WHERE id=$1 FOR SHARE`, dep.ID, "UPDATE deployment_sidecar_layers%"
			} else if kind == "secret" {
				prefix = "UPDATE app_secrets%"
			}
			if _, err := hold.Exec(ctx, query, key); err != nil {
				t.Fatal(err)
			}
			writer := make(chan error, 1)
			go func() {
				var err error
				if kind == "variable" {
					_, err = pool.Exec(ctx, `UPDATE app_envs SET value='edited' WHERE app_id=$1 AND scope='stage' AND key='MODE'`, f.app.ID)
				} else if kind == "sidecar" {
					_, err = pool.Exec(ctx, `UPDATE deployment_sidecar_layers SET storage_key='apps/edited.ext4' WHERE deployment_id=$1 AND sidecar_name='helper'`, dep.ID)
				} else {
					_, err = pool.Exec(ctx, `UPDATE app_secrets SET ciphertext=$2 WHERE app_id=$1 AND scope='stage' AND key='TOKEN'`, f.app.ID, []byte("edited"))
				}
				writer <- err
			}()
			deadline, tick := time.NewTimer(5*time.Second), time.NewTicker(10*time.Millisecond)
			defer deadline.Stop()
			defer tick.Stop()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE $2 AND $1=ANY(pg_blocking_pids(pid)))`, blocker, prefix).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-writer:
					t.Fatalf("config writer bypassed parent fence: %v", err)
				case <-deadline.C:
					t.Fatal("writer never waited on parent fence")
				case <-tick.C:
				}
			}
			published := make(chan error, 1)
			go func() { _, err := store.PublishOwnedInstanceRuntime(ctx, p); published <- err }()
			select {
			case err := <-published:
				if err != nil {
					t.Fatalf("publisher could not read committed capture: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("publisher waited on a tuple held by its blocked writer")
			}
			if err := hold.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-writer:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("writer stuck after publication")
			}
			stored, err := store.InstanceRuntimeConfigFence(ctx, f.account.ID, f.app.ID, p.InstanceID)
			if err != nil || stored != p.ConfigFence {
				t.Fatalf("publication lost original capture: %v", err)
			}
			snapshot, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			current, err := state.NewRuntimeAppConfigFence(snapshot)
			if err != nil || current == stored {
				t.Fatal("committed config writer did not invalidate stored capture")
			}
		})
	}
}
