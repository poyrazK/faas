package meter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// Record terms before sampling. The first captured contract starts now; it
// cannot price old usage retroactively. Every evidence row retains its version.
func (l *Loop) recordFinancialPricing(ctx context.Context, now time.Time) error {
	store, ok := l.store.(state.FinancialStore)
	if !ok {
		return nil
	}
	accounts, err := l.store.ListAllAccounts(ctx)
	if err != nil {
		return fmt.Errorf("financial pricing accounts: %w", err)
	}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	var failures []error
	for _, account := range accounts {
		if !account.Plan.Valid() {
			failures = append(failures, fmt.Errorf("financial pricing: %w", state.ErrInvalidArgument))
			continue
		}
		prices, err := store.ListFinancialPriceSnapshots(ctx, account.ID, start)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		compute := financial.Price{Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: int64(account.Plan.PlanIncludedGBHours()) * api.SecondsPerGBHour}
		if account.Plan == api.PlanFree {
			compute.MillicentsPerUnit = 0
		}
		contracts := []state.FinancialPriceSnapshot{{Price: compute, DeliveryMode: "live"}}
		egress := state.FinancialPriceSnapshot{Price: financial.Price{Meter: "egress", Currency: "EUR", Unit: "interface_bytes", UnitQuantity: 1}, DeliveryMode: "off"}
		if policies, ok := l.pusher.(billing.MeterUsagePolicyProvider); ok {
			if policy, configured := policies.MeterUsagePolicy(account.Plan, state.BillingMeterEgress); configured && !policy.EffectiveFrom.After(now) {
				if err := validateMeterUsagePolicy(policy); err != nil {
					failures = append(failures, err)
					continue
				}
				egress.Price.UnitQuantity = policy.UnitQuantity
				egress.Price.MillicentsPerUnit = policy.MillicentsPerUnit
				egress.Price.IncludedQuantity = policy.IncludedQuantity
				egress.DeliveryMode = string(policy.Mode)
			}
		}
		contracts = append(contracts, egress)
		for _, p := range contracts {
			p.AccountID, p.Plan = account.ID, account.Plan
			p.PeriodStart, p.PeriodEnd = start, start.AddDate(0, 1, 0)
			p.EffectiveFrom = now.UTC().Truncate(time.Minute)
			var latest state.FinancialPriceSnapshot
			for _, old := range prices {
				if old.Price.Meter == p.Price.Meter && (latest.Price.Version == "" || old.EffectiveFrom.After(latest.EffectiveFrom) || (old.EffectiveFrom.Equal(latest.EffectiveFrom) && old.RecordedAt.After(latest.RecordedAt))) {
					latest = old
				}
			}
			terms := latest.Price
			terms.Version = ""
			if terms == p.Price && latest.Plan == p.Plan && latest.DeliveryMode == p.DeliveryMode {
				continue
			}
			encoded, err := json.Marshal(struct {
				Plan          api.Plan
				Mode          string
				Price         financial.Price
				EffectiveFrom time.Time
			}{p.Plan, p.DeliveryMode, p.Price, p.EffectiveFrom})
			if err != nil {
				failures = append(failures, err)
				continue
			}
			digest := sha256.Sum256(encoded)
			p.Price.Version = "financial-v1-" + hex.EncodeToString(digest[:])
			found := false
			for _, old := range prices {
				if old.Price.Version == p.Price.Version && old.Price.Meter == p.Price.Meter {
					found = true
					break
				}
			}
			if found {
				continue
			}
			if _, err := store.PutFinancialPriceSnapshot(ctx, p); err != nil {
				// A second meterd can capture the same immutable terms in an
				// earlier minute. Its earlier contract is authoritative.
				if errors.Is(err, state.ErrConflict) {
					captured, readErr := store.ListFinancialPriceSnapshots(ctx, account.ID, start)
					if readErr == nil {
						for _, old := range captured {
							if old.Price == p.Price && old.Plan == p.Plan && old.DeliveryMode == p.DeliveryMode {
								found = true
								break
							}
						}
					}
				}
				if !found {
					failures = append(failures, err)
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (l *Loop) recordFinancialSample(ctx context.Context, sampler *Sampler, now time.Time, sampleErr error) error {
	store, ok := l.store.(state.FinancialStore)
	if !ok {
		return nil
	}
	minute := MinuteKey(now)
	if _, exact := sampler.store.(instanceBillingSecondsStore); exact {
		minute = now.UTC().Truncate(time.Minute).Add(-time.Minute)
	}
	// Interface observations have delayed/missing-source semantics. A successful
	// compute sampler alone cannot establish complete transfer coverage.
	return store.RecordFinancialSamplingWindow(ctx, minute, sampleErr == nil, false)
}
