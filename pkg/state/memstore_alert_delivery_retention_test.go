package state_test

// adr: 20261029203000 — alert deliveries past their retention are purged
// oldest first in bounded batches; a rule's newest delivery and an unsettled
// one stay.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStorePurgeAlertDeliveriesBefore(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	rule, err := m.CreateAlertRule(ctx, memSampleRule(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fire := func(key string, at time.Time, settle bool) string {
		t.Helper()
		id, won, err := m.ClaimAlertFire(ctx, rule.ID, key, []byte(`{}`), 1, at)
		if err != nil || !won {
			t.Fatalf("claim %s: won=%v err=%v", key, won, err)
		}
		if settle {
			if err := m.UpdateAlertDeliveryStatus(ctx, id, state.AlertDeliveryDelivered, 1, 200, "", &at); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	fire("old-1", base, true)
	fire("old-2", base.Add(time.Hour), true)
	pending := fire("old-pending", base.Add(2*time.Hour), false)
	newest := fire("newest", base.Add(3*time.Hour), true)

	cutoff := base.Add(24 * time.Hour) // every delivery is older
	if n, err := m.PurgeAlertDeliveriesBefore(ctx, cutoff, 1); err != nil || n != 1 {
		t.Fatalf("first batch = %d, %v; want 1 (the batch bound)", n, err)
	}
	if n, err := m.PurgeAlertDeliveriesBefore(ctx, cutoff, 10); err != nil || n != 1 {
		t.Fatalf("second batch = %d, %v; want the one settled delivery left", n, err)
	}
	left, err := m.ListAlertDeliveriesForRule(ctx, rule.ID, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, d := range left {
		kept[d.ID] = true
	}
	if len(left) != 2 || !kept[pending] || !kept[newest] {
		t.Fatalf("kept %+v, want the pending and the newest delivery", left)
	}
}
