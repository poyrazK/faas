package managedpostgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresPendingOverageUsesStablePages(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	other, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@overage-page.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var want int
	for _, entry := range []struct {
		subject, provider string
		month             time.Time
	}{
		{accountID, "polar", month}, {other.ID, "polar", month}, {accountID, "polar", month.AddDate(0, 1, 0)},
		{accountID, "another", month}, {accountID, "polar", month.AddDate(0, 4, 0)},
	} {
		p := OveragePendingStep{Provider: entry.provider, Live: true, Previous: OverageDelivery{Provider: entry.provider, SubjectID: entry.subject, PeriodStart: entry.month}, Step: OverageChargeStep{SubjectID: entry.subject, PeriodStart: entry.month, At: entry.month.Add(time.Hour), TargetMillicents: hour}}
		if reserved, err := store.ReserveOverage(ctx, p); err != nil || !reserved {
			t.Fatal("reserve", reserved, err)
		}
		if entry.provider == "polar" && !entry.month.After(month.AddDate(0, 1, 0)) {
			want++
		}
	}
	var after OveragePendingRef
	count := 0
	for {
		page, err := store.PendingOverage(ctx, "polar", after, month.AddDate(0, 2, 0), 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if after.SubjectID != "" && compareOverageRefs(after, page[0]) >= 0 {
			t.Fatal("repeated or skipped cursor", after, page)
		}
		pending, found, err := store.OveragePending(ctx, "polar", page[0].SubjectID, page[0].PeriodStart)
		if err != nil || !found {
			t.Fatal("pending disappeared", found, err)
		}
		if recorded, err := store.CompleteOverage(ctx, pending); err != nil || !recorded {
			t.Fatal("complete", recorded, err)
		}
		after = page[0]
		count++
	}
	if count != want {
		t.Fatalf("deleting completed entries skipped later pages: got=%d want=%d", count, want)
	}
	if page, err := store.PendingOverage(ctx, "another", OveragePendingRef{}, month, 10); err != nil || len(page) != 1 {
		t.Fatalf("another provider's step was lost: %+v %v", page, err)
	}
}
