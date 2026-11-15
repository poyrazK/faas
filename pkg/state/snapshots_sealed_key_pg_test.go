//go:build !no_pg

// adr: 20261110210000
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 3d-3: a sealed snapshot records its key and capture id together,
// bound to the storage key's capture token; neither changes afterwards; a
// per-node compute login records snapshots only of deployments that ran on
// its node.
func TestSnapshotsSealedKey(t *testing.T) {
	store, owner := lifecycleCompletionPGStore(t)
	ctx := t.Context()
	nodeLogin, login := ownerAuthorityLogin(t, owner, false)
	a := completionNode(t, store, state.NodeLifecycleActive)
	if _, err := owner.Exec(ctx, `insert into compute_node_db_logins (login, node_name) values ($1, $2)`, login, a.Name); err != nil {
		t.Fatal(err)
	}
	acct, err := store.CreateAccount(ctx, "sealed-snapshot@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "sealed-snapshot", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:sealed"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func(token string, key []byte, id string) state.Snapshot {
		return state.Snapshot{DeploymentID: dep.ID, FCVersion: "fc-1", MemBytes: 1 << 20, DiskBytes: 4096,
			StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, token), Tier: state.SnapshotTierInit,
			SealedKey: key, SealedCaptureID: id}
	}

	token := uuid.NewString()
	created, err := store.CreateSnapshot(ctx, snapshot(token, []byte("sealed-key"), token))
	if err != nil {
		t.Fatalf("sealed snapshot: %v", err)
	}
	if string(created.SealedKey) != "sealed-key" || created.SealedCaptureID != token {
		t.Fatalf("recorded seal = %q %q, want sealed-key %s", created.SealedKey, created.SealedCaptureID, token)
	}
	live, err := store.LatestSnapshotForTier(ctx, dep.ID, state.SnapshotTierInit)
	if err != nil || string(live.SealedKey) != "sealed-key" || live.SealedCaptureID != token {
		t.Fatalf("read back = %+v, %v", live, err)
	}
	plainSnap := snapshot(uuid.NewString(), nil, "")
	plainSnap.Tier = state.SnapshotTierWarm // one live row per tier
	if plain, err := store.CreateSnapshot(ctx, plainSnap); err != nil || len(plain.SealedKey) != 0 || plain.SealedCaptureID != "" {
		t.Fatalf("plaintext snapshot = %+v, %v", plain, err)
	}

	// The store refuses a half or misbound seal; so does the schema.
	for name, snap := range map[string]state.Snapshot{
		"key without id": snapshot(uuid.NewString(), []byte("k"), ""),
		"id without key": func() state.Snapshot { tok := uuid.NewString(); return snapshot(tok, nil, tok) }(),
		"other id":       snapshot(uuid.NewString(), []byte("k"), uuid.NewString()),
	} {
		if _, err := store.CreateSnapshot(ctx, snap); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("%s = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := owner.Exec(ctx, `insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, storage_key, sealed_key, sealed_capture_id)
		values ($1, 'fc-1', 1, 1, $2, '\x01', $3)`, dep.ID, state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, uuid.NewString()), uuid.NewString()); sqlState(err) != "23514" {
		t.Errorf("misbound insert = %v, want SQLSTATE 23514", err)
	}

	// Nobody changes a seal afterwards, the schema owner included.
	for name, sql := range map[string]string{
		"replace key": `update snapshots set sealed_key = '\x02' where id = $1`,
		"clear":       `update snapshots set sealed_key = null, sealed_capture_id = null where id = $1`,
	} {
		if _, err := owner.Exec(ctx, sql, created.ID); sqlState(err) != "42501" {
			t.Errorf("%s = %v, want SQLSTATE 42501", name, err)
		}
	}
	if _, err := owner.Exec(ctx, `update snapshots set stale = true where id = $1`, created.ID); err != nil {
		t.Fatalf("an unrelated update: %v", err)
	}

	// A per-node login records snapshots only of deployments that ran on its node.
	byNode := state.NewPgStore(nodeLogin)
	tok := uuid.NewString()
	if _, err := byNode.CreateSnapshot(ctx, snapshot(tok, []byte("forged"), tok)); sqlState(err) != "42501" {
		t.Fatalf("per-node snapshot of a deployment that never ran there = %v, want SQLSTATE 42501", err)
	}
	if _, err := owner.Exec(ctx, `insert into instances (app_id, deployment_id, state, ram_mb, node_id) values ($1, $2, 'snapshotting', 512, $3)`,
		app.ID, dep.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := byNode.CreateSnapshot(ctx, snapshot(tok, []byte("ran-here"), tok)); err != nil {
		t.Fatalf("per-node snapshot of a deployment that ran there: %v", err)
	}
}
