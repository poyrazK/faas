package billing

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// ManagedPostgresLineItem is the provider-neutral mapping consumed by a
// future invoice sink. Product codes are stable across Neon, another hosted
// provider, and an in-house operator; ProviderCostMillicents is retained for
// COGS reconciliation and is never presented as a customer overage charge.
type ManagedPostgresLineItem struct {
	AccountID              string
	PeriodStart            time.Time
	Code                   string
	Meter                  managedpostgres.Meter
	Unit                   string
	Quantity               int64
	ProviderCostMillicents int64
}

// ManagedPostgresUsageLineItems turns one complete monthly snapshot into the
// immutable line-item shape that invoice/COGS adapters can publish. Bundled
// plans have no overage line: an invoice adapter may attach these codes to the
// included managed-postgres product for margin reporting.
func ManagedPostgresUsageLineItems(accountID string, snapshot managedpostgres.UsageSnapshot, policy managedpostgres.UsagePolicy) ([]ManagedPostgresLineItem, error) {
	if accountID == "" || snapshot.PeriodStart.IsZero() {
		return nil, errors.New("billing: invalid managed postgres usage snapshot")
	}
	items, err := policy.LineItems(snapshot)
	if err != nil {
		return nil, err
	}
	out := make([]ManagedPostgresLineItem, 0, len(items))
	for _, item := range items {
		out = append(out, ManagedPostgresLineItem{
			AccountID: accountID, PeriodStart: snapshot.PeriodStart.UTC(), Code: item.Code,
			Meter: item.Meter, Unit: item.Unit, Quantity: item.Quantity,
			ProviderCostMillicents: item.CostMillicents,
		})
	}
	return out, nil
}
