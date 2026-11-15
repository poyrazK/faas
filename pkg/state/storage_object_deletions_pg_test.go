//go:build !no_pg

// adr: 20261115180000
package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// A compute node only requests deletes; requests come due after their grace;
// outcomes are recorded for the checked request; the reference check keeps
// live snapshots and layers.
func TestStorageObjectDeletions(t *testing.T) {
	store, owner := lifecycleCompletionPGStore(t)
	ctx := t.Context()
	nodeLogin, login := ownerAuthorityLogin(t, owner, false)
	a := completionNode(t, store, state.NodeLifecycleActive)
	if _, err := owner.Exec(ctx, `insert into compute_node_db_logins (login, node_name) values ($1, $2)`, login, a.Name); err != nil {
		t.Fatal(err)
	}
	node := state.NewPgStore(nodeLogin)

	key := "snap/" + uuid.NewString() + "/captures/" + uuid.NewString() + "/v2/mem"
	if err := node.RequestStorageObjectDeletion(ctx, key, "imaged"); err != nil {
		t.Fatalf("node request: %v", err)
	}
	var by string
	var due time.Time
	if err := owner.QueryRow(ctx, `select requested_by, next_attempt_at from storage_object_deletions where storage_key = $1`, key).Scan(&by, &due); err != nil {
		t.Fatal(err)
	}
	if by != a.Name || time.Until(due) < 9*time.Minute {
		t.Fatalf("requested_by=%q due in %v, want the node's name and a 10 min grace", by, time.Until(due))
	}
	if got, _ := store.DueStorageObjectDeletions(ctx, time.Now(), 10); len(got) != 0 {
		t.Fatalf("due before the grace: %v", got)
	}
	got, err := store.DueStorageObjectDeletions(ctx, time.Now().Add(11*time.Minute), 10)
	if err != nil || len(got) != 1 || got[0].StorageKey != key {
		t.Fatalf("due after the grace = %v, %v", got, err)
	}

	// The node cannot decide, hurry or remove its request.
	for name, sql := range map[string]string{
		"decide": `update storage_object_deletions set state = 'deleted', decided_at = now() where storage_key = $1`,
		"hurry":  `update storage_object_deletions set next_attempt_at = now() - interval '1 hour' where storage_key = $1`,
	} {
		if _, err := nodeLogin.Exec(ctx, sql, key); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var state string
		var next time.Time
		if err := owner.QueryRow(ctx, `select state, next_attempt_at from storage_object_deletions where storage_key = $1`, key).Scan(&state, &next); err != nil {
			t.Fatal(err)
		}
		if state != "requested" || time.Until(next) < 9*time.Minute {
			t.Errorf("%s by the node left state=%s next in %v, want a fresh request", name, state, time.Until(next))
		}
	}
	if _, err := nodeLogin.Exec(ctx, `delete from storage_object_deletions where storage_key = $1`, key); sqlState(err) != "42501" {
		t.Errorf("node delete = %v, want SQLSTATE 42501", err)
	}

	// An outcome applies to the request that was checked, not a newer one.
	got, _ = store.DueStorageObjectDeletions(ctx, time.Now().Add(11*time.Minute), 10)
	checked := got[0]
	if err := node.RequestStorageObjectDeletion(ctx, key, "imaged"); err != nil {
		t.Fatal(err)
	}
	if err := store.DecideStorageObjectDeletion(ctx, checked, state.StorageObjectDeleted, ""); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := owner.QueryRow(ctx, `select state from storage_object_deletions where storage_key = $1`, key).Scan(&st); err != nil || st != "requested" {
		t.Fatalf("after a stale decision state = %s, %v; want the newer request kept", st, err)
	}
	got, _ = store.DueStorageObjectDeletions(ctx, time.Now().Add(11*time.Minute), 10)
	if err := store.RetryStorageObjectDeletion(ctx, got[0], time.Now().Add(time.Hour), "referenced"); err != nil {
		t.Fatal(err)
	}
	if again, _ := store.DueStorageObjectDeletions(ctx, time.Now().Add(11*time.Minute), 10); len(again) != 0 {
		t.Fatalf("a retried request came due early: %v", again)
	}
	if err := store.DecideStorageObjectDeletion(ctx, got[0], state.StorageObjectDeleted, ""); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `select state from storage_object_deletions where storage_key = $1`, key).Scan(&st); err != nil || st != "deleted" {
		t.Fatalf("state = %s, %v; want deleted", st, err)
	}

	// References: a live snapshot keeps its whole capture; a live deployment
	// keeps its layer; platform and unknown keys are never free.
	acct, err := store.CreateAccount(ctx, "object-gc@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "object-gc", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:gc"})
	if err != nil {
		t.Fatal(err)
	}
	capture := uuid.NewString()
	mem := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, capture)
	snap, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "fc-1", MemBytes: 1, DiskBytes: 1, StorageKey: mem, Tier: state.SnapshotTierInit})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{
		mem: true,
		state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem}) + ".age":            true,
		state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, uuid.NewString()): false,
		storagekey.AppLayer("object-gc", dep.ID):                                      true,
		storagekey.AppLayer("object-gc", uuid.NewString()):                            false,
		"base/base-amd64.ext4": true,
		"sbom/whatever.json":   true,
	} {
		ref, class, err := store.StorageObjectReferenced(ctx, key)
		if err != nil || ref != want {
			t.Errorf("%s referenced = %v (%s), %v; want %v", key, ref, class, err, want)
		}
	}
	if _, err := owner.Exec(ctx, `update snapshots set delete_pending = true where id = $1`, snap.ID); err != nil {
		t.Fatal(err)
	}
	if ref, _, err := store.StorageObjectReferenced(ctx, mem); err != nil || ref {
		t.Fatalf("delete-pending snapshot still referenced = %v, %v", ref, err)
	}
}
