// adr: 20261025115958

package object

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestInventoryWaitsForAdmittedDeletions pins ADR-20261025115958: inventory
// lists only once deletions admitted before its claim settle, and gives the
// claim up when they do not, so waiting deletions continue at once.
func TestInventoryWaitsForAdmittedDeletions(t *testing.T) {
	ctx := t.Context()
	st := state.NewMemStore()
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "drain", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "physical-drain", BackendID: "external", BackendFingerprint: "fingerprint", Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	b.State = "ready"
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	begin := func(key string) (state.ObjectDeletion, error) {
		j, _, err := st.BeginObjectDeletion(ctx, state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: key}, AccountID: account.ID, AppID: app.ID, Token: uuid.NewString()}, policy)
		return j, err
	}
	settled, err := begin("settles")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ClaimObjectInventory(ctx, b.ID, "scan"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		settled.State, settled.LastErrorCode = "failed", "preparation_failed"
		_, _ = st.FinishObjectDeletion(ctx, settled)
	}()
	if err = awaitObjectDeletions(ctx, st, b, 5*time.Second); err != nil {
		t.Fatal("inventory did not wait for the deletion to settle", err)
	}

	// A deletion that never settles gives the claim up after the drain.
	if err = st.ReleaseObjectInventory(ctx, b.ID, "scan"); err != nil {
		t.Fatal(err)
	}
	if _, err = begin("stuck"); err != nil {
		t.Fatal(err)
	}
	if err = st.ClaimObjectInventory(ctx, b.ID, "again"); err != nil {
		t.Fatal(err)
	}
	if _, err = begin("waiting"); !errors.Is(err, state.ErrObjectBucketBusy) {
		t.Fatal("deletion did not yield to the claim", err)
	}
	if err = awaitObjectDeletions(ctx, st, b, 100*time.Millisecond); !errors.Is(err, errObjectDeletionsUnsettled) {
		t.Fatal("unsettled deletion", err)
	}
	releaseObjectInventory(ctx, st, b.ID, "again")
	if _, err = begin("waiting"); err != nil {
		t.Fatal("deletion still waited for a released claim", err)
	}
}
