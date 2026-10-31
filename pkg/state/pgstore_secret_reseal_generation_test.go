//go:build !no_pg

// adr: 1741 — a reseal rewrites only ciphertext and stamps no runtime change
// (ADR-1139), yet schedd's owner cache holds that ciphertext, so the app's
// runtime generation moves and the cache drops the pre-reseal envelope.
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgSecretResealMovesTheRuntimeGeneration(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	generation := func() int64 {
		t.Helper()
		g, err := store.AppRuntimeGeneration(ctx, f.app.ID)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	reseal := func(statement string, args ...any) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SELECT set_config('faas.release_secret_rekey','on',true)"); err != nil {
			t.Fatal(err)
		}
		tag, err := tx.Exec(ctx, statement, args...)
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("reseal %q: %v (%d rows)", statement, err, tag.RowsAffected())
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	secrets, ok := any(store).(state.ProjectEnvironmentSecretStore)
	if !ok {
		t.Fatalf("%T does not keep project environment secrets", store)
	}
	if _, err := secrets.SetProjectEnvironmentSecret(ctx, state.ProjectEnvironmentSecret{AccountID: f.account.ID, ProjectID: f.project.ID,
		EnvironmentSlug: "production", Key: "STRIPE_KEY", Ciphertext: []byte("sealed-before"), Kid: "age1resealtestkid", ValueHash: "hash-1"}, 10); err != nil {
		t.Fatal(err)
	}
	before := generation()
	reseal("UPDATE project_environment_secrets SET ciphertext=$1, kid='age1resealtestnext' WHERE project_id=$2 AND environment_slug='production' AND key='STRIPE_KEY'",
		[]byte("sealed-after"), f.project.ID)
	afterProject := generation()
	if afterProject <= before {
		t.Fatalf("project secret reseal left the generation at %d (was %d)", afterProject, before)
	}

	dep := f.deployments["default"]
	if _, err := pool.Exec(ctx, `INSERT INTO deployment_release_secret_snapshots(deployment_id,values_json)
		VALUES($1,'[{"key":"API_KEY","kid":"age1resealtestkid","ciphertext":"YmVmb3Jl"}]')`, dep.ID); err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := generation()
	reseal(`UPDATE deployment_release_secret_snapshots SET values_json='[{"key":"API_KEY","kid":"age1resealtestnext","ciphertext":"YWZ0ZXI="}]' WHERE deployment_id=$1`, dep.ID)
	if after := generation(); after <= beforeSnapshot {
		t.Fatalf("release snapshot reseal left the generation at %d (was %d)", after, beforeSnapshot)
	}
}
