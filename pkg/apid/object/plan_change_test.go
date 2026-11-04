// adr: 20261101214152

package object

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A downgrade is checked against the target plan's storage ceiling, or the one
// support granted, with the spend cap the account would hold after the
// change: a default cap follows the plan, Free has none, and a cap the
// customer chose stays.
func TestPlanChangeStorageUsesTheCapAfterTheChange(t *testing.T) {
	st, registry, bucket, _ := receiptRecoveryFixture(t, "http://127.0.0.1:9")
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	ctx := t.Context()
	const gib = int64(1 << 30)
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(ctx, bucket.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, bucket.ID, token, 100*gib, 1); err != nil {
		t.Fatal(err)
	}
	account, err := st.AccountByID(ctx, bucket.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	proDefault, _ := api.PlanPro.DefaultOverageCapCents()
	var grants state.ObjectStorageCeilingGrantStore = st
	for _, tc := range []struct {
		name    string
		cap     int64
		grant   int64
		target  api.Plan
		task    string
		ceiling int64
	}{
		{name: "a default cap follows the plan, whose ceiling binds", cap: proDefault, target: api.PlanHobby,
			task: "delete 80 GiB of object storage: the hobby plan stores at most 20 GiB and you store 100 GiB, or ask support for a higher storage ceiling", ceiling: 20 * gib},
		{name: "free stops at its own ceiling", cap: proDefault, target: api.PlanFree,
			task: "delete 99 GiB of object storage: the free plan stores at most 1 GiB and you store 100 GiB", ceiling: gib},
		{name: "a cap the customer chose stays and can bind", cap: 0, target: api.PlanHobby,
			task: "delete 90 GiB of object storage: the hobby plan stores at most 10 GiB and you store 100 GiB, or raise your spend cap", ceiling: 10 * gib},
		{name: "a support grant replaces the plan's ceiling", cap: 5_000, grant: 150 * gib, target: api.PlanHobby, ceiling: 150 * gib},
		{name: "free ignores a support grant", cap: proDefault, grant: 150 * gib, target: api.PlanFree,
			task: "delete 99 GiB of object storage: the free plan stores at most 1 GiB and you store 100 GiB", ceiling: gib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := st.UpdateAccountOverageCapCents(ctx, account.ID, &tc.cap); err != nil {
				t.Fatal(err)
			}
			if err := grants.ClearObjectStorageCeilingGrant(ctx, account.ID); err != nil {
				t.Fatal(err)
			}
			if tc.grant > 0 {
				if err := grants.SetObjectStorageCeilingGrant(ctx, state.ObjectStorageCeilingGrant{
					OwnerID: account.ID, MaxBytes: tc.grant, Reason: "support_ticket", GrantedBy: uuid.NewString(), GrantedAt: time.Now(),
				}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := svc.PlanChangeStorage(ctx, account, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			if got.Task != tc.task || got.CeilingBytes != tc.ceiling || got.StoredBytes != 100*gib {
				t.Fatalf("PlanChangeStorage(%s) = %+v\nwant task %q, ceiling %d", tc.target, got, tc.task, tc.ceiling)
			}
		})
	}
	if got, err := (&Service{Host: receiptRecoveryHost{store: st}}).PlanChangeStorage(ctx, account, api.PlanFree); err != nil || got != (PlanChangeStorage{}) {
		t.Fatalf("without object storage = %+v, %v", got, err)
	}
}
