package billing

import (
	"context"
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

// ManagedPostgresChargePolicy is the rollout boundary for managed PostgreSQL
// overage charges (ADR-20261031110037). EffectiveFrom is a UTC month; Live
// false is shadow: steps are recorded and never sent.
type ManagedPostgresChargePolicy struct {
	Provider      string
	Live          bool
	EffectiveFrom time.Time
}

// ManagedPostgresChargeStep raises one usage subject's month charge from
// DeliveredMillicents to TargetMillicents.
type ManagedPostgresChargeStep struct {
	SubjectID           string
	PeriodStart         time.Time
	At                  time.Time
	UsedUnitSeconds     int64
	AllowanceSeconds    int64
	BilledUnitSeconds   int64
	DeliveredMillicents int64
	TargetMillicents    int64
}

// ManagedPostgresChargeSink is the provider seam for managed PostgreSQL
// overage. Implementations bill TargetMillicents - DeliveredMillicents and key
// their idempotency on the subject, the month and TargetMillicents. Off is a
// policy returning ok=false.
type ManagedPostgresChargeSink interface {
	ManagedPostgresChargePolicy() (ManagedPostgresChargePolicy, bool)
	PublishManagedPostgresCharge(ctx context.Context, step ManagedPostgresChargeStep) error
}
