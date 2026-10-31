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
	"github.com/prometheus/client_golang/prometheus"
)

// Record terms before sampling. The first captured contract starts now; it
// cannot price old usage retroactively. Every evidence row retains its version.
func (l *Loop) recordFinancialPricing(ctx context.Context, now time.Time) error {
	store, ok := l.store.(state.FinancialStore)
	if !ok {
		return nil
	}
	accounts, err := state.ListBillingResourceAccounts(ctx, l.store)
	if err != nil {
		return fmt.Errorf("financial pricing accounts: %w", err)
	}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	var failures []error
	// Terms this process already found stored for an account this month are
	// not read again: unchanged terms cost no query per account per minute
	// (ADR-20261029180000). Accounts no longer listed drop out of the cache.
	priced := make(map[string]string, len(accounts))
	defer func() { l.pricedTerms = priced }()
	for _, account := range accounts {
		if !account.Plan.Valid() {
			failures = append(failures, fmt.Errorf("financial pricing: %w", state.ErrInvalidArgument))
			continue
		}
		compute := financial.Price{Meter: "compute", Currency: financial.BillingCurrency, Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: int64(account.Plan.PlanIncludedGBHours()) * api.SecondsPerGBHour}
		if account.Plan == api.PlanFree {
			compute.MillicentsPerUnit = 0
		}
		contracts := []state.FinancialPriceSnapshot{{Price: compute, DeliveryMode: "live"}}
		egress := state.FinancialPriceSnapshot{Price: financial.Price{Meter: "egress", Currency: financial.BillingCurrency, Unit: "interface_bytes", UnitQuantity: 1}, DeliveryMode: "off"}
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
		key := fmt.Sprintf("%s|%s|%+v", start.Format(time.RFC3339), account.Plan, contracts)
		if l.pricedTerms[account.ID] == key {
			priced[account.ID] = key
			continue
		}
		prices, err := store.ListFinancialPriceSnapshots(ctx, account.ID, start)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		failed := len(failures)
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
		if len(failures) == failed {
			priced[account.ID] = key
		}
	}
	return errors.Join(failures...)
}

func (l *Loop) recordFinancialSample(ctx context.Context, sampler *Sampler, now time.Time, sampleErr, pricingErr error) error {
	store, ok := l.store.(state.FinancialStore)
	if !ok {
		return nil
	}
	// Interface observations have delayed/missing-source semantics. A successful
	// compute sampler alone cannot establish complete transfer coverage.
	if _, exact := sampler.store.(instanceBillingSecondsStore); !exact {
		return store.RecordFinancialSamplingWindow(ctx, MinuteKey(now), sampleErr == nil && pricingErr == nil, false)
	}
	// Every minute both samplers rolled in full (H5-55 catch-up included) is
	// recorded complete on the first tick whose price capture succeeds. The
	// minute is never rolled again, so a failed pricing or record write only
	// delays its record; leaving it unrecorded held provider delivery for
	// every account until it aged out of the catch-up window
	// (ADR-20261029180000). compute_complete only ever turns true.
	newest := sampler.newestClosedMinute(now.UTC())
	var errs []error
	newestRecorded := false
	if pricingErr == nil {
		for i, minute := range sampler.UnrecordedMinutes() {
			if i > api.MeterCatchUpMinutesPerTick {
				break
			}
			if err := store.RecordFinancialSamplingWindow(ctx, minute, true, false); err != nil {
				errs = append(errs, err)
				continue
			}
			sampler.MarkRecorded(minute)
			newestRecorded = newestRecorded || minute.Equal(newest)
		}
	}
	if !newestRecorded {
		errs = append(errs, store.RecordFinancialSamplingWindow(ctx, newest, false, false))
	}
	return errors.Join(errs...)
}

// newUsageFinalLagGauge registers meterd_usage_final_lag_seconds: how far the
// sample tick's clock is past the oldest closed minute of the catch-up window
// not recorded compute-complete. The provider pusher delivers only the hours
// before that minute (ADR-1162), so a growing value is usage held back from
// every account (ADR-20261029180000). Healthy ticks read 75-135 s with the
// roll settle (ADR-20261030120000).
func newUsageFinalLagGauge(reg prometheus.Registerer) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "meterd_usage_final_lag_seconds",
		Help: "Seconds between the sample tick and the oldest closed minute of the catch-up window not recorded compute-complete; provider delivery stops at that minute (ADR-20261029180000).",
	})
	if err := reg.Register(g); err != nil {
		var dup prometheus.AlreadyRegisteredError
		if errors.As(err, &dup) {
			if existing, ok := dup.ExistingCollector.(prometheus.Gauge); ok {
				return existing
			}
		}
	}
	return g
}
