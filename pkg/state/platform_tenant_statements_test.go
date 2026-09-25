package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemPlatformTenantStatementExpectedPriorStatus(t *testing.T) {
	m := NewMemStore()
	accountID, tenantID, appID, consumerID, rateID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	m.platformTenants[tenantID] = PlatformTenant{ID: tenantID, AccountID: accountID}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	in := PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Revision: 1, Currency: "EUR",
		BillableUnits: 2, AmountMillicents: 20, AsOf: time.Now().UTC(),
		Lines: []PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumerID, WindowStart: start,
			BillableUnits: 2, RateCardID: rateID, Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
	}
	first, created, err := m.CreatePlatformTenantStatement(context.Background(), in)
	if err != nil || !created {
		t.Fatalf("initial statement: %v, %v", created, err)
	}
	if _, _, err := m.FinalizePlatformTenantStatement(context.Background(), accountID, tenantID, first.ID); err != nil {
		t.Fatal(err)
	}
	in.Revision = 2
	in.PriorStatus = APIConsumerUsageStatementDraft // stale snapshot read before finalization
	if _, _, err := m.CreatePlatformTenantStatement(context.Background(), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale prior status err=%v", err)
	}
	in.PriorStatus = APIConsumerUsageStatementFinalized
	if _, created, err := m.CreatePlatformTenantStatement(context.Background(), in); err != nil || !created {
		t.Fatalf("fresh adjustment: %v, %v", created, err)
	}
}
