// adr: 20261101214152

package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectStorageCeilingFollowsSpendCapMem(t *testing.T) {
	objectStorageSpendCapCeilingSuite(t, state.NewMemStore())
}

func TestObjectStorageCeilingFollowsSpendCapPG(t *testing.T) {
	st, _ := pgStore(t)
	objectStorageSpendCapCeilingSuite(t, st)
}

// A paid account's storage ceiling is its included storage plus what its
// spend cap buys. Above it, writes stop and object bodies are refused, while
// HEAD-like reads without a body still pass; clearing the cap lifts it.
func objectStorageSpendCapCeilingSuite(t *testing.T, st contractTestStore) {
	t.Helper()
	ctx := context.Background()
	const gib = int64(1 << 30)
	b := seedRecoveryBucket(t, st) // a Pro account: 50 GiB included
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	p := gatewaySafetyPolicy()
	p.MaxAccountBytes, p.MaxBucketBytes = 1<<40, 1<<40 // the operator's ceiling sits above the plan's
	zero := int64(0)
	if err := st.UpdateAccountOverageCapCents(ctx, b.AccountID, &zero); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, b.ID, token, 60*gib, 1); err != nil {
		t.Fatal(err)
	}

	d, err := st.ObjectReadAdmission(ctx, b.AccountID, b.ID, time.Now(), p)
	var limit *state.ObjectStorageLimitError
	if err != nil || d.Err != nil || !errors.As(d.DownloadErr, &limit) || !errors.Is(d.DownloadErr, state.ErrObjectBudget) ||
		limit.Kind != "account_bytes" || limit.Limit != 50*gib || limit.Observed != 60*gib {
		t.Fatalf("decision with 60 GiB under a zero cap's 50 GiB ceiling = %+v (%+v), %v", d, limit, err)
	}
	if err := st.ReserveObjectStorageGatewayEgress(ctx, b.ID, 1, time.Now(), p); !errors.As(err, &limit) || limit.Kind != "account_bytes" {
		t.Fatalf("a download above the ceiling = %v", err)
	}
	if err := st.ReserveObjectStorageGatewayEgress(ctx, b.ID, 0, time.Now(), p); err != nil {
		t.Fatalf("a read without a body above the ceiling = %v", err)
	}
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "new", 1, true, p); !errors.Is(err, state.ErrObjectCapacity) {
		t.Fatalf("a write above the ceiling = %v", err)
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.PlanLimits.MaxAccountBytes != 50*gib {
		t.Fatalf("snapshot plan limits = %+v, %v", usage.PlanLimits, err)
	}

	// Clearing the cap leaves the operator's ceiling, so downloads resume.
	if err := st.UpdateAccountOverageCapCents(ctx, b.AccountID, nil); err != nil {
		t.Fatal(err)
	}
	if d, err = st.ObjectReadAdmission(ctx, b.AccountID, b.ID, time.Now(), p); err != nil || d.Err != nil || d.DownloadErr != nil {
		t.Fatalf("decision without a cap = %+v, %v", d, err)
	}
	if err := st.ReserveObjectStorageGatewayEgress(ctx, b.ID, 1, time.Now(), p); err != nil {
		t.Fatalf("a download without a cap = %v", err)
	}
}
