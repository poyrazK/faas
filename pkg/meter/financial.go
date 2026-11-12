package meter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	var head int64
	if history, ok := l.store.(state.PlanActivationStore); ok {
		head, err = history.BillingPlanActivationHead(ctx)
		if err != nil {
			return fmt.Errorf("financial plan activation head: %w", err)
		}
	}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	priced := make(map[string]string, len(accounts))
	defer func() { l.pricedTerms = priced }()
	var failures []error
	for _, account := range accounts {
		contracts, err := l.financialContracts(account.Plan, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		key := fmt.Sprintf("%s|%s|%+v|%d", start.Format(time.RFC3339), account.Plan, contracts, head)
		if l.pricedTerms[account.ID] == key {
			priced[account.ID] = key
			continue
		}
		prices, err := store.ListFinancialPriceSnapshots(ctx, account.ID, start)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		prices, err = l.replayFinancialPlanActivations(ctx, store, account, start, now, prices)
		if err == nil {
			_, err = l.captureFinancialContracts(ctx, store, account.ID, account.Plan, start, now, 0, contracts, prices)
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		priced[account.ID] = key
	}
	return errors.Join(failures...)
}

// planChangeEffectiveFrom dates a newly observed plan (ADR-20261103121407).
// meterd notices a plan change on its next pricing pass, which is late when
// meterd was down or catching up, and the hours between would get the old
// plan's allowance, Free status and cap. The subject's recorded change time is
// used instead, at minute precision, but never before the previous terms took
// effect (snapshots stay ordered) nor after observed. A store without the
// change time, or a subject whose change time is unknown, keeps observed.
func planChangeEffectiveFrom(ctx context.Context, store state.Store, subjectID string, previous, observed time.Time) (time.Time, error) {
	reader, ok := store.(state.PlanChangeReader)
	if !ok {
		return observed, nil
	}
	changed, known, err := reader.PlanChangedAt(ctx, subjectID)
	if err != nil {
		return time.Time{}, fmt.Errorf("financial pricing plan change time: %w", err)
	}
	if !known {
		return observed, nil
	}
	at := changed.UTC().Truncate(time.Minute)
	if at.Before(previous) {
		at = previous
	}
	if at.After(observed) {
		at = observed
	}
	return at, nil
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
	// With cumulative, checkpointed network counters the same minute is
	// egress-complete as well (ADR-20261101113742); without them nothing ever
	// recorded egress coverage, so egress budgets never projected and strict
	// egress could never activate.
	newest := sampler.newestClosedMinute(now.UTC())
	egressComplete := sampler.cumulativeNetwork()
	var errs []error
	newestRecorded := false
	if pricingErr == nil {
		for i, minute := range sampler.UnrecordedMinutes() {
			if i > api.MeterCatchUpMinutesPerTick {
				break
			}
			if err := store.RecordFinancialSamplingWindow(ctx, minute, true, egressComplete); err != nil {
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
