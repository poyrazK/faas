// adr: 20261101214152

package object

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A downgrade is checked against the target plan's storage ceiling, with the
// spend cap the account would hold after the change: a default cap follows
// the plan, Free has none, and a cap the customer chose stays.
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
	for _, tc := range []struct {
		name    string
		cap     int64
		target  api.Plan
		task    string
		ceiling int64
	}{
		{"a default cap follows the plan", proDefault, api.PlanHobby,
			"delete 24 GiB of object storage: the hobby plan stores at most 76 GiB and you store 100 GiB, or raise your spend cap", 76 * gib},
		{"free stops at its own ceiling", proDefault, api.PlanFree,
			"delete 99 GiB of object storage: the free plan stores at most 1 GiB and you store 100 GiB", gib},
		{"a cap the customer chose stays", 5_000, api.PlanHobby, "", 10*gib + 1_666*gib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := st.UpdateAccountOverageCapCents(ctx, account.ID, &tc.cap); err != nil {
				t.Fatal(err)
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
