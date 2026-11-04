// adr: 20261026120843
package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSpendCapHoldsRoundTripOnBothStores(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		holds, ok := store.(SpendCapHoldStore)
		if !ok {
			t.Fatalf("%T does not keep spend cap holds", store)
		}
		acct, err := store.CreateAccount(t.Context(), "cap-"+time.Now().Format("150405.000000")+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		if _, found, err := holds.GetSpendCapHold(t.Context(), acct.ID); err != nil || found {
			t.Fatalf("hold before any write: found=%v err=%v", found, err)
		}
		for _, bad := range []SpendCapHold{
			{AccountID: acct.ID, Month: month, CapCents: 100, ObservedCents: 99},
			{AccountID: acct.ID, Month: month.Add(time.Hour), CapCents: 100, ObservedCents: 100},
			{AccountID: acct.ID, Month: month, CapCents: -1, ObservedCents: 0},
		} {
			if err := holds.PutSpendCapHold(t.Context(), bad); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("PutSpendCapHold(%+v) = %v; want ErrInvalidArgument", bad, err)
			}
		}
		if err := holds.PutSpendCapHold(t.Context(), SpendCapHold{AccountID: acct.ID, Month: month, CapCents: 100, ObservedCents: 120}); err != nil {
			t.Fatal(err)
		}
		first, found, err := holds.GetSpendCapHold(t.Context(), acct.ID)
		if err != nil || !found || first.CapCents != 100 || first.ObservedCents != 120 || !first.Month.UTC().Equal(month) {
			t.Fatalf("hold = %+v found=%v err=%v", first, found, err)
		}
		// A later tick for the same month and cap keeps the original reached_at.
		if err := holds.PutSpendCapHold(t.Context(), SpendCapHold{AccountID: acct.ID, Month: month, CapCents: 100, ObservedCents: 150}); err != nil {
			t.Fatal(err)
		}
		again, _, _ := holds.GetSpendCapHold(t.Context(), acct.ID)
		if again.ObservedCents != 150 || !again.ReachedAt.Equal(first.ReachedAt) {
			t.Fatalf("re-recorded hold = %+v; want observed 150 and reached_at %v", again, first.ReachedAt)
		}
		if !again.Holds(100, month.Add(48*time.Hour)) || again.Holds(200, month.Add(48*time.Hour)) || again.Holds(100, month.AddDate(0, 1, 0)) {
			t.Fatal("hold must apply only to its cap and month")
		}
		if err := holds.ClearSpendCapHold(t.Context(), acct.ID); err != nil {
			t.Fatal(err)
		}
		if _, found, err := holds.GetSpendCapHold(t.Context(), acct.ID); err != nil || found {
			t.Fatalf("hold after clear: found=%v err=%v", found, err)
		}
	})
}
