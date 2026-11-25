package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresMaskedBranchReadinessRequiresCopyEvidence(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := postgresReadyDatabase(t, store, accountID, "source", now.Add(-time.Minute))
	input := postgresTestDatabase(accountID, "masked-copy", now)
	input.OrgID = source.OrgID
	input.RestoreSourceDatabaseID = source.ID
	input.Spec = MaskedBranchSpec(source.Spec)
	input.ExpiresAt = now.Add(time.Hour)
	policy := &maskedBranchPolicy{sourceResourceID: source.ProviderResourceID, tables: []api.ManagedPostgresMaskedTable{{
		Name: "customers", Columns: []api.ManagedPostgresMaskedColumn{{Name: "email", Action: "email"}},
	}}}
	branch, created, err := store.reserve(reserveAsBranch(ctx, 2), input, 2, policy)
	if err != nil || !created || !branch.IsMaskedBranch() || !branch.InBranchAllowance() {
		t.Fatalf("reserve masked branch: created=%t, branch=%+v, err=%v", created, branch, err)
	}
	stored, err := store.maskedBranchPolicy(ctx, branch.ID)
	if err != nil || stored.validated || stored.sourceResourceID != source.ProviderResourceID || len(stored.tables) != 1 {
		t.Fatalf("reservation did not persist its unvalidated policy: %+v, %v", stored, err)
	}
	claimed, err := store.Claim(ctx, accountID, branch.ID, uuid.NewString(), StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, branch.ID, claimed.LeaseToken, branch.ID, now); err != nil {
		t.Fatal(err)
	}
	claimed.ProviderResourceID = branch.ID
	// An ordinary reconciler cannot publish the provisioned VM before the
	// masking worker records validation, even with the active lease.
	if _, err := store.FinishProvision(ctx, branch.ID, claimed.LeaseToken, now); err == nil {
		t.Fatal("branch became ready before validation")
	}
	actual, err := store.Get(ctx, accountID, branch.ID)
	if err != nil || actual.State != StateProvisioning {
		t.Fatalf("unvalidated branch state: %s, %v", actual.State, err)
	}
	stale := claimed
	stale.LeaseToken = uuid.NewString()
	if err := store.recordMaskedBranchValidation(ctx, stale); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale worker recorded evidence: %v", err)
	}
	if err := store.recordMaskedBranchValidation(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	ready, err := store.FinishProvisionWithDataResource(ctx, claimed, ObservedDatabase{
		ProviderResourceID: branch.ID, DataResourceID: branch.ID, Spec: branch.Spec, Status: ProviderStatusReady,
	}, now)
	if err != nil || ready.State != StateReady || !ready.IsMaskedBranch() {
		t.Fatalf("validated branch was not published: %+v, %v", ready, err)
	}
}
