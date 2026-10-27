// adr: 20261023223000

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestVersioningBaselineObservation pins H9-S135/S158: GCS reports a
// never-versioned bucket as Suspended, and the first read of that status froze
// the bucket's writes and deletes for 15 minutes.
func TestVersioningBaselineObservationMem(t *testing.T) {
	versioningBaselineSuite(t, state.NewMemStore())
}
func TestVersioningBaselineObservationPG(t *testing.T) {
	st, _ := pgStore(t)
	versioningBaselineSuite(t, st)
}

func versioningBaselineSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	v := st.(state.ObjectBucketVersioningStore)
	b, _ := seedAccounting(t, st)
	j, err := v.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended")
	if err != nil || j.PropagationUntil == nil || j.PropagationUntil.After(time.Now()) || !j.VersionsRequired || j.ObservedStatus != "Suspended" {
		t.Fatal("baseline observation fenced the bucket", j, err)
	}
	// The all-version inventory can start at once instead of after 15 minutes.
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "baseline")
	if err != nil || j.Token == "" {
		t.Fatal("baseline transition could not start", j, err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Suspended")
	if err != nil || j.State != "inventory" {
		t.Fatal("baseline transition waited out a propagation fence", j, err)
	}

	// An enabled bucket may hold versions that are still propagating.
	enabled, _ := seedAccounting(t, st)
	j, err = v.ObserveObjectBucketVersioning(ctx, enabled.AccountID, enabled.AppID, enabled.ID, "Enabled")
	if err != nil || j.PropagationUntil == nil || !j.PropagationUntil.After(time.Now().Add(14*time.Minute)) {
		t.Fatal("enabled observation skipped the propagation fence", j, err)
	}

	// Suspended after Gregale changed versioning is not a baseline.
	changed, _ := seedAccounting(t, st)
	if _, err = v.RequestObjectBucketVersioning(ctx, changed.AccountID, changed.AppID, changed.ID, "Enabled"); err != nil {
		t.Fatal(err)
	}
	j, err = v.ObserveObjectBucketVersioning(ctx, changed.AccountID, changed.AppID, changed.ID, "Suspended")
	if err != nil || j.PropagationUntil == nil || !j.PropagationUntil.After(time.Now().Add(14*time.Minute)) {
		t.Fatal("observation after a requested change skipped the fence", j, err)
	}
}
