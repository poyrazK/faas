//go:build !no_pg

// adr: 531
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeAppValuesScopeGrantsAndLifetime(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeAppValuesScopeGrantsAndLifetime(t, store)
}

func TestPgRuntimeAppValuesReadOneSnapshot(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "PRIVATE", []byte("original-sealed")); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM project_environments WHERE account_id=$1 AND project_id=$2 AND slug='stage'`, f.account.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_environments(account_id,project_id,slug) VALUES ($1,$2,'stage')`, f.account.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app_envs SET value='replacement-private' WHERE app_id=$1 AND scope='stage'`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app_secrets SET ciphertext=$2 WHERE app_id=$1 AND scope='stage'`, f.app.ID, []byte("replacement-sealed")); err != nil {
		t.Fatal(err)
	}
	got, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, dep.ID)
	if err != nil || len(got.Values) != 1 || got.Values[0].Value != "stage" || len(got.Secrets) != 1 || string(got.Secrets[0].Ciphertext) != "original-sealed" {
		t.Fatalf("uncommitted values escaped: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) || len(got.Secrets) != 0 || len(got.Values) != 0 {
		t.Fatalf("replacement secrets escaped: %v", err)
	}
}
