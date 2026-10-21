//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentPolicyJournalPG(t *testing.T) {
	store, _ := pgStore(t)
	testDeploymentPolicyJournal(t, store)
}

func TestDeploymentPolicyJournalPGConflicts(t *testing.T) {
	for _, mode := range []string{"cleanup-first", "policy-edit", "aba", "same-value", "new-deploy", "changed-intent", "wrong-app", "wrong-account"} {
		t.Run(mode, func(t *testing.T) { store, _ := pgStore(t); testDeploymentPolicyConflict(t, store, mode) })
	}
}

func TestDeploymentPolicyJournalPGConcurrency(t *testing.T) {
	store, _ := pgStore(t)
	testPolicyStageCleanupRace(t, store)
}

func TestDeploymentPolicyJournalPGSameDesired(t *testing.T) {
	store, _ := pgStore(t)
	testPolicySameDesired(t, store)
}

func TestDeploymentPolicyJournalPGManifestWriterTrigger(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, p := stagingFixture(t, store)
	if _, err := store.ApplyDeploymentStaging(ctx, app.ID, p); err != nil {
		t.Fatal(err)
	}
	// A writer outside generic app PATCH still relinquishes policy ownership.
	if _, err := pool.Exec(ctx, `UPDATE apps SET manifest=jsonb_set(manifest,'{profiling}','{"enabled":true,"window_seconds":20}') WHERE id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET manifest=jsonb_set(manifest,'{profiling}','{"enabled":true,"window_seconds":10}') WHERE id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	p.Request.Operation = "restore"
	if _, err := store.ApplyDeploymentStaging(ctx, app.ID, p); !errors.Is(err, state.ErrDeploymentStagingConflict) {
		t.Fatalf("trigger failed to fence ABA: %v", err)
	}
	snap, err := store.DeploymentStagingSnapshot(ctx, app.AccountID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]any
	_ = json.Unmarshal(snap.Settings["profiling"], &policy)
	if policy["enabled"] != true || snap.Revision <= p.Request.ExpectedRevision+1 {
		t.Fatalf("snapshot=%+v", snap)
	}
}
