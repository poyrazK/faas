package meter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/state"
)

// UsageSource is what the provider pusher quantifies billed hours from
// (ADR-20261107105126). usage_minutes and the financial ledger are written in
// one transaction per sampled minute; the ledger is the record customers and
// operators read, so it becomes the billing source once a closed month and a
// sandbox month show the two agree.
type UsageSource string

const (
	// UsageSourceOff bills usage_minutes and compares nothing.
	UsageSourceOff UsageSource = "off"
	// UsageSourceLedgerShadow bills usage_minutes and compares every
	// delivered hour with the ledger. It is the default.
	UsageSourceLedgerShadow UsageSource = "ledger_shadow"
	// UsageSourceLedger bills the ledger and compares every delivered hour
	// with usage_minutes.
	UsageSourceLedger UsageSource = "ledger"
)

// ParseUsageSource reads [meter] billing_usage_source; empty is the default,
// ledger_shadow.
func ParseUsageSource(v string) (UsageSource, error) {
	switch s := UsageSource(v); s {
	case "":
		return UsageSourceLedgerShadow, nil
	case UsageSourceOff, UsageSourceLedgerShadow, UsageSourceLedger:
		return s, nil
	}
	return "", fmt.Errorf("billing usage source %q: want off, ledger_shadow or ledger", v)
}

// WithUsageSource sets what p bills from; a Pusher bills usage_minutes, with
// no comparison, until it is set.
func (p *Pusher) WithUsageSource(s UsageSource) *Pusher {
	p.source = s
	return p
}

// receiptSource is the usage source p's receipts record.
func (p *Pusher) receiptSource() string {
	if p.source == UsageSourceLedger {
		return state.UsageSourceLedger
	}
	return state.UsageSourceUsageMinutes
}

// usage reads a subject's usage from the source p bills from.
func (p *Pusher) usage() usageReader {
	return usageReader{store: p.store, ledger: p.source == UsageSourceLedger}
}

// errNoLedgerUsage reports a store that keeps no hourly ledger.
var errNoLedgerUsage = errors.New("meter: store reads no ledger usage")

// usageReader reads a subject's usage from usage_minutes or, when ledger is
// set, from the financial ledger. Ranges count whole minutes the way
// usage_minutes does: a minute m is in [start, end) when start <= m < end.
type usageReader struct {
	store  state.Store
	ledger bool
}

// sum is accountID's usage of meter in [start, end).
func (u usageReader) sum(ctx context.Context, accountID string, meter state.BillingMeter, start, end time.Time) (int64, error) {
	if !u.ledger {
		if meter == state.BillingMeterCompute {
			return sumUsageRows(ctx, u.store, accountID, start, end)
		}
		return sumMeterUsageRows(ctx, u.store, accountID, meter, start, end)
	}
	rows, err := u.hourly(ctx, accountID, meter, start, end)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, row := range rows {
		if total, err = addUsageQuantity(total, row.Quantity); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// hourly is accountID's usage of meter in [start, end) by UTC hour.
func (u usageReader) hourly(ctx context.Context, accountID string, meter state.BillingMeter, start, end time.Time) ([]state.HourQuantity, error) {
	if !u.ledger {
		reader, ok := u.store.(state.HourlyMeterUsageReader)
		if !ok {
			return nil, errNoHourlyUsage
		}
		if !start.Before(end) {
			return nil, nil
		}
		return reader.HourlyMeterUsage(ctx, accountID, meter, start, end)
	}
	reader, ok := u.store.(state.FinancialHourlyUsageReader)
	if !ok {
		return nil, errNoLedgerUsage
	}
	// Evidence covers [source_start, source_start+1m): the minutes from
	// start's minute on, through the minute before end, match usage_minutes.
	start, end = ceilMinute(start), ceilMinute(end)
	if !start.Before(end) {
		return nil, nil
	}
	return reader.FinancialHourlyUsage(ctx, accountID, string(meter), start, end, math.MaxInt64)
}

func ceilMinute(t time.Time) time.Time {
	t = t.UTC()
	if down := t.Truncate(time.Minute); !down.Equal(t) {
		return down.Add(time.Minute)
	}
	return t
}

// windowQuantity is the quantity p bills for one pending window of meter:
// acct's usage in the hour from its billing_from on. usageMinutes is the
// window's usage_minutes quantity. In ledger_shadow and ledger modes the two
// sources are compared and a mismatch counted and logged; the source p bills
// from decides. A ledger that cannot be read holds the window in ledger mode
// and is only logged in shadow mode.
func (p *Pusher) windowQuantity(ctx context.Context, grants *monthGrants, acct state.Account, meter state.BillingMeter, provider string, hour time.Time, usageMinutes int64) (int64, error) {
	if p.source != UsageSourceLedgerShadow && p.source != UsageSourceLedger {
		return usageMinutes, nil
	}
	start, err := grants.billingFrom(ctx, acct.ID, provider)
	switch {
	case errors.Is(err, state.ErrNotFound):
		start = hour // a legacy subject without an identity row has no boundary
	case err != nil && p.source == UsageSourceLedger:
		return 0, err
	case err != nil:
		p.log.Warn("meter: billing boundary unavailable for comparison", "account", acct.ID, "meter", meter, "hour", hour, "err", err)
		return usageMinutes, nil
	}
	if start.Before(hour) {
		start = hour
	}
	ledger, err := usageReader{store: p.store, ledger: true}.sum(ctx, acct.ID, meter, start, hour.Add(time.Hour))
	if err != nil {
		if p.source == UsageSourceLedger {
			return 0, fmt.Errorf("meter: ledger usage for %s at %s: %w", acct.ID, hour.Format(time.RFC3339), err)
		}
		p.log.Warn("meter: ledger usage unavailable for comparison", "account", acct.ID, "meter", meter, "hour", hour, "err", err)
		return usageMinutes, nil
	}
	billed := string(p.source)
	counters := p.sourceCounters()
	counters.compared.WithLabelValues(string(meter), billed).Inc()
	if ledger != usageMinutes {
		counters.mismatched.WithLabelValues(string(meter), billed).Inc()
		p.log.Warn("meter: billing usage sources disagree", "account", acct.ID, "meter", meter, "hour", hour,
			"source", billed, "usage_minutes_quantity", usageMinutes, "ledger_quantity", ledger)
	}
	if p.source == UsageSourceLedger {
		return ledger, nil
	}
	return usageMinutes, nil
}

// usageSourceCounters are meterd_billing_usage_source_compared_windows_total
// and meterd_billing_usage_source_mismatch_windows_total.
type usageSourceCounters struct {
	compared, mismatched *prometheus.CounterVec
}

func (p *Pusher) sourceCounters() usageSourceCounters {
	return usageSourceCounters{
		compared: registerCounterVec(p.ops.Registry(), prometheus.CounterOpts{
			Name: "meterd_billing_usage_source_compared_windows_total",
			Help: "Delivered usage windows (account-hours) whose usage_minutes and ledger quantities the pusher compared, by meter and the usage source it bills from (ADR-20261107105126).",
		}),
		mismatched: registerCounterVec(p.ops.Registry(), prometheus.CounterOpts{
			Name: "meterd_billing_usage_source_mismatch_windows_total",
			Help: "Compared usage windows whose usage_minutes and ledger quantities differed, by meter and the usage source the pusher bills from (ADR-20261107105126). Any increase blocks moving billing to the ledger.",
		}),
	}
}

func registerCounterVec(reg prometheus.Registerer, opts prometheus.CounterOpts) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(opts, []string{"meter", "source"})
	if err := reg.Register(c); err != nil {
		var dup prometheus.AlreadyRegisteredError
		if errors.As(err, &dup) {
			if existing, ok := dup.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
	}
	return c
}

// recordReceipt settles acct's hour of meter with quantity, recording the
// usage source that quantified it where the store keeps one.
func (p *Pusher) recordReceipt(ctx context.Context, provider, accountID string, meter state.BillingMeter, hour time.Time, quantity int64) error {
	if recorder, ok := p.store.(state.BillingDeliverySourceRecorder); ok {
		return recorder.RecordBillingDeliveryFromSource(ctx, provider, accountID, meter, hour, quantity, p.receiptSource())
	}
	if meter == state.BillingMeterCompute {
		return p.store.RecordBillingUsageDelivery(ctx, provider, accountID, hour, quantity)
	}
	return p.store.RecordBillingMeterUsageDelivery(ctx, provider, accountID, meter, hour, quantity)
}
