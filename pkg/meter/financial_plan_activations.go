package meter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) financialContracts(plan api.Plan, now time.Time) ([]state.FinancialPriceSnapshot, error) {
	if !plan.Valid() {
		return nil, state.ErrInvalidArgument
	}
	compute := financial.Price{Meter: "compute", Currency: financial.BillingCurrency, Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: api.IncludedMBSeconds(plan)}
	if plan == api.PlanFree {
		compute.MillicentsPerUnit = 0
	}
	contracts := []state.FinancialPriceSnapshot{{Price: compute, DeliveryMode: "live"}}
	egress := state.FinancialPriceSnapshot{Price: financial.Price{Meter: "egress", Currency: financial.BillingCurrency, Unit: "interface_bytes", UnitQuantity: 1}, DeliveryMode: "off"}
	if provider, ok := l.pusher.(billing.MeterUsagePolicyProvider); ok {
		if policy, configured := provider.MeterUsagePolicy(plan, state.BillingMeterEgress); configured && !policy.EffectiveFrom.After(now) {
			if err := validateMeterUsagePolicy(policy); err != nil {
				return nil, err
			}
			egress.Price.UnitQuantity = policy.UnitQuantity
			egress.Price.MillicentsPerUnit = policy.MillicentsPerUnit
			egress.Price.IncludedQuantity = policy.IncludedQuantity
			egress.DeliveryMode = string(policy.Mode)
		}
	}
	return append(contracts, egress), nil
}

// Replay every activation before deciding that unchanged current terms can be
// cached. The replay includes the previous month while pending usage can still
// be delivered, and survives both meterd cache loss and process restart.
func (l *Loop) replayFinancialPlanActivations(ctx context.Context, store state.FinancialStore, account state.Account, start, now time.Time, current []state.FinancialPriceSnapshot) ([]state.FinancialPriceSnapshot, error) {
	history, ok := l.store.(state.PlanActivationStore)
	if !ok {
		return current, nil
	}
	prior := start.AddDate(0, -1, 0)
	activations, err := history.ListBillingPlanActivations(ctx, account.ID, prior, now)
	if err != nil {
		return nil, err
	}
	// The sequence records causal plan writes; wall-clock timestamps can
	// regress. Replay in write order and preserve the prior activation's
	// lower bound, rather than installing a later plan before its predecessor.
	sort.Slice(activations, func(i, j int) bool { return activations[i].Sequence < activations[j].Sequence })
	var previous time.Time
	byMonth := map[time.Time][]state.FinancialPriceSnapshot{start: current}
	for _, a := range activations {
		if a.At.Before(previous) {
			a.At = previous
		}
		previous = a.At
		month := time.Date(a.At.UTC().Year(), a.At.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
		prices, loaded := byMonth[month]
		if !loaded {
			prices, err = store.ListFinancialPriceSnapshots(ctx, account.ID, month)
			if err != nil {
				return nil, err
			}
		}
		if len(prices) == 0 {
			// From is authoritative even when this is the first plan capture.
			terms, err := l.financialContracts(a.From, a.At)
			if err != nil {
				return nil, err
			}
			prices, err = l.captureFinancialContracts(ctx, store, account.ID, a.From, month, month, 0, terms, prices)
			if err != nil {
				return nil, err
			}
		}
		terms, err := l.financialContracts(a.To, a.At)
		if err != nil {
			return nil, err
		}
		prices, err = l.captureFinancialContracts(ctx, store, account.ID, a.To, month, a.At, a.Sequence, terms, prices)
		if err != nil {
			return nil, err
		}
		byMonth[month] = prices
	}
	return byMonth[start], nil
}

func (l *Loop) captureFinancialContracts(ctx context.Context, store state.FinancialStore, account string, plan api.Plan, month, observed time.Time, activation int64, contracts, prices []state.FinancialPriceSnapshot) ([]state.FinancialPriceSnapshot, error) {
	for _, p := range contracts {
		p.AccountID, p.Plan = account, plan
		p.PeriodStart, p.PeriodEnd = month, month.AddDate(0, 1, 0)
		p.EffectiveFrom = observed.UTC().Truncate(time.Minute)
		var latest state.FinancialPriceSnapshot
		for _, old := range prices {
			if old.Price.Meter == p.Price.Meter && (latest.Price.Version == "" || old.EffectiveFrom.After(latest.EffectiveFrom) || old.EffectiveFrom.Equal(latest.EffectiveFrom) && old.RecordedAt.After(latest.RecordedAt)) {
				latest = old
			}
		}
		terms := latest.Price
		terms.Version = ""
		if activation == 0 && terms == p.Price && latest.Plan == plan && latest.DeliveryMode == p.DeliveryMode {
			continue
		}
		if activation == 0 && latest.Price.Version != "" && latest.Plan != plan {
			at, err := planChangeEffectiveFrom(ctx, l.store, account, latest.EffectiveFrom, p.EffectiveFrom)
			if err != nil {
				return nil, err
			}
			p.EffectiveFrom = at
		}
		encoded, err := json.Marshal(struct {
			Plan          api.Plan
			Mode          string
			Price         financial.Price
			EffectiveFrom time.Time
			Activation    int64 `json:",omitempty"`
		}{p.Plan, p.DeliveryMode, p.Price, p.EffectiveFrom, activation})
		if err != nil {
			return nil, err
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
		captured, err := store.PutFinancialPriceSnapshot(ctx, p)
		if errors.Is(err, state.ErrConflict) {
			// A replica may have captured this immutable version first.
			rows, readErr := store.ListFinancialPriceSnapshots(ctx, account, month)
			if readErr != nil {
				return nil, readErr
			}
			for _, old := range rows {
				if old.Price == p.Price && old.Plan == p.Plan && old.DeliveryMode == p.DeliveryMode {
					captured, err = old, nil
					break
				}
			}
		}
		if err != nil {
			return nil, err
		}
		prices = append(prices, captured)
	}
	return prices, nil
}
