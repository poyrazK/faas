// Package reconciler is the push↔usage drift detector (ADR-049 §B.1).
//
// The reconciler is the read-only complement to meterd's pusher
// (pkg/meter/pusher.go). Every FAAS_RECONCILE_INTERVAL it walks
// every paid billing subject (accounts and company organizations), sums the
// pusher's delivery receipts (billing_usage_deliveries) for the last 24
// complete hours, by delivery time for a provider that stamps usage when it
// is sent (Polar) and by usage hour otherwise, and asks the active
// billing.Provider for the matching summary. The diff between delivered and provider totals is exposed as
// Prometheus gauges so the BillingDrift alert pages when events the pusher
// recorded as delivered are missing or duplicated at the provider (revenue
// leakage / over-billing).
//
// Receipts, not usage, are the local side (ADR-20261031161842): an hour not
// yet final, an hour above the spend cap, and a suspended subject's hours are
// all correctly undelivered, and comparing usage made each of them page. The
// ledger dual run (pkg/meter.LedgerReconciler) owns "receipts vs what should
// have been billed".
//
// Per-account failures are fail-soft: a network blip or provider rate-limit
// is logged, counted, and does not block the rest of the fleet. A provider
// without reconciliation is different: the supported gauge is set to zero
// and the operator alert treats drift as unknown rather than healthy.
//
// The reconciler owns its own Prometheus registry (separate from
// wire.OpsMetrics) so it can be wired in cmd/meterd/main.go
// alongside the existing gauges without coupling to the ops-
// metrics struct. The two registries are merged at the /metrics
// scrape by promhttp.HandlerFor.
package reconciler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reconciler compares the pusher's delivery receipts against the active
// billing.Provider's totals. The store is read-only against
// billing_usage_deliveries; the provider is read-only against the billing
// surface. No external state mutation happens here.
type Reconciler struct {
	Store    state.Store
	Provider billing.Provider
	Log      *slog.Logger

	// Registry is the Prometheus registry the reconciler emits
	// gauges into. Required; cmd/meterd/main.go creates a fresh
	// registry and merges it with the ops-metrics registry at
	// the /metrics scrape endpoint.
	Registry *prometheus.Registry

	// Window is the rolling window the reconciler compares
	// local-vs-pushed totals over. Default 24 h when zero. Only complete
	// UTC hours are included, matching provider delivery windows.
	Window time.Duration

	// ProviderName is the label value on the emitted gauges
	// (e.g. "stripe", "paddle"). Empty defaults to "unknown".
	ProviderName string
	Mode         billing.Mode
	now          func() time.Time

	driftMBSeconds *prometheus.GaugeVec
	driftRatio     *prometheus.GaugeVec
	supported      *prometheus.GaugeVec
	modeEnabled    *prometheus.GaugeVec
	failures       *prometheus.CounterVec
}

// DefaultInterval is the cadence the reconciler runs at when the
// caller passes 0 for Interval. Matches the metering cadence
// (every 6 h) so the BillingDrift alert has a fresh signal within
// 1 h of the next sample tick.
const DefaultInterval = 6 * time.Hour

// DefaultWindow is the rolling window the reconciler compares
// local-vs-pushed totals over. 24 h matches the audit's
// recommended drift-detection horizon.
const DefaultWindow = 24 * time.Hour

// New constructs a Reconciler and registers its gauges against
// Registry. If Registry is nil, a fresh registry is created so the
// caller can pass nil in tests.
func New(providerName string, store state.Store, provider billing.Provider, log *slog.Logger, registry *prometheus.Registry) *Reconciler {
	if registry == nil {
		registry = prometheus.NewRegistry()
	}
	if providerName == "" {
		providerName = "unknown"
	}
	r := &Reconciler{
		Store:        store,
		Provider:     provider,
		Log:          log,
		Registry:     registry,
		ProviderName: providerName,
		Window:       DefaultWindow,
		now:          time.Now,
		driftMBSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meterd_billing_drift_mb_seconds",
			Help: "Signed diff between the mb_seconds meterd recorded as delivered (billing_usage_deliveries) and the provider's total over the rolling Window. ADR-049 §B.1, ADR-20261031161842.",
		}, []string{"account_id", "provider"}),
		driftRatio: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meterd_billing_drift_ratio",
			Help: "abs(delivered-provider) / max(delivered, provider) over the rolling Window. ADR-049 §B.1, ADR-20261031161842. The BillingDrift alert pages on ratio > 0.005 for 1h.",
		}, []string{"account_id", "provider"}),
		supported: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meterd_billing_reconcile_supported",
			Help: "Whether the active billing provider supports usage reconciliation (1 supported, 0 unavailable).",
		}, []string{"provider"}),
		modeEnabled: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meterd_billing_mode_enabled",
			Help: "Whether provider billing and reconciliation are enabled by deployment configuration (1 live, 0 disabled).",
		}, []string{"provider"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meterd_billing_drift_reconcile_failures_total",
			Help: "Billing reconciliation failures by provider and failure source. A non-zero rate means the drift gauges may be stale and requires operator investigation.",
		}, []string{"provider", "reason"}),
	}
	registry.MustRegister(r.driftMBSeconds, r.driftRatio, r.supported, r.modeEnabled, r.failures)
	capabilityValue := float64(0)
	if provider != nil && provider.Capabilities().Has(billing.CapUsageReconcile) {
		capabilityValue = 1
	}
	r.supported.WithLabelValues(providerName).Set(capabilityValue)
	r.modeEnabled.WithLabelValues(providerName).Set(1)
	return r
}

// WithMode applies the deployment billing switch. The zero/default remains
// live for callers created before FAAS_BILLING_MODE existed.
func (r *Reconciler) WithMode(mode billing.Mode) *Reconciler {
	r.Mode = mode.Effective()
	if r.Mode.Enabled() {
		r.modeEnabled.WithLabelValues(r.ProviderName).Set(1)
	} else {
		r.modeEnabled.WithLabelValues(r.ProviderName).Set(0)
	}
	return r
}

// Handler returns the Prometheus scrape handler for the reconciler's
// gauges. cmd/meterd mounts this on a separate path or merges
// registries — both shapes are supported.
func (r *Reconciler) Handler() http.Handler {
	return promhttp.HandlerFor(r.Registry, promhttp.HandlerOpts{})
}

// RunOnce is one pass over every paid billing subject. The reconciler does
// not iterate per-app, only per-subject, so the cost is O(paid_subjects).
func (r *Reconciler) RunOnce(ctx context.Context) error {
	if r.Window == 0 {
		r.Window = DefaultWindow
	}
	if r.ProviderName == "" {
		r.ProviderName = "unknown"
	}
	mode := r.Mode.Effective()
	if !mode.Enabled() {
		r.modeEnabled.WithLabelValues(r.ProviderName).Set(0)
		return nil
	}
	r.modeEnabled.WithLabelValues(r.ProviderName).Set(1)
	if r.Provider == nil || !r.Provider.Capabilities().Has(billing.CapUsageReconcile) {
		r.supported.WithLabelValues(r.ProviderName).Set(0)
		r.failures.WithLabelValues(r.ProviderName, "unsupported").Inc()
		if r.Log != nil {
			r.Log.Error("billing reconciliation unavailable", "provider", r.ProviderName)
		}
		return nil
	}
	r.supported.WithLabelValues(r.ProviderName).Set(1)
	deliveries, ok := r.Store.(state.BillingDeliveryReader)
	if !ok {
		r.failures.WithLabelValues(r.ProviderName, "store").Inc()
		return errors.New("reconciler: store does not record billing deliveries")
	}
	// Company organizations are billing subjects of their own (#6029); the
	// pusher delivers their hours, so their drift is measured too.
	accounts, err := state.ListBillingResourceAccounts(ctx, r.Store)
	if err != nil {
		r.failures.WithLabelValues(r.ProviderName, "store").Inc()
		return fmt.Errorf("list accounts: %w", err)
	}
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	start, end := billing.CompletedUsageWindow(now, r.Window)
	if !start.Before(end) {
		return nil
	}
	for _, acct := range accounts {
		if err := r.reconcileOne(ctx, deliveries, acct, start, end); err != nil {
			// Fail-soft per account: log + skip, but retain a fleet-level
			// counter so provider outages cannot masquerade as a healthy
			// zero-drift scrape.
			reason := "account"
			var classified *reconcileError
			if errors.As(err, &classified) {
				reason = classified.reason
			}
			r.failures.WithLabelValues(r.ProviderName, reason).Inc()
			if r.Log != nil {
				r.Log.Warn("reconcile account failed",
					"account_id", acct.ID, "err", err)
			}
		}
	}
	return nil
}

type reconcileError struct {
	reason string
	err    error
}

func (e *reconcileError) Error() string { return e.err.Error() }
func (e *reconcileError) Unwrap() error { return e.err }

func (r *Reconciler) reconcileOne(ctx context.Context, deliveries state.BillingDeliveryReader, acct state.Account, start, end time.Time) error {
	// Skip non-paid plans — no Stripe / Paddle surface to drift
	// against. The metered quota math is identical but the
	// provider-side count is zero by construction.
	if acct.Plan == api.PlanFree {
		return nil
	}
	identity, err := r.Store.BillingIdentity(ctx, acct.ID, r.ProviderName)
	if err != nil {
		return &reconcileError{reason: "identity", err: fmt.Errorf("load %s billing identity: %w", r.ProviderName, err)}
	}
	acct.ProviderCustomerID = identity.CustomerID
	acct.StripeSubscriptionItem = identity.SubscriptionID
	if identity.BillingFrom.After(start) {
		start = identity.BillingFrom.UTC()
	}
	if !start.Before(end) {
		return nil
	}
	if r.stampsUsageAtDelivery() {
		return r.reconcileDelivered(ctx, acct, start, end)
	}
	// A provider that stamps usage at its hour gets each delivery at the
	// start of that hour, including the first hour after BillingFrom, so both
	// sides read whole hours.
	start = start.UTC().Truncate(time.Hour)
	// Receipts are what the pusher handed the provider: an hour that is not
	// final yet, is above the spend cap, or belongs to a suspended subject
	// has none, and the provider has nothing for it either
	// (ADR-20261031161842).
	delivered, err := deliveries.DeliveredComputeMBSeconds(ctx, r.ProviderName, acct.ID, start, end)
	if err != nil {
		return &reconcileError{reason: "usage", err: fmt.Errorf("delivered usage: %w", err)}
	}
	pushed, err := r.Provider.ReconcileUsage(ctx, acct, start, end)
	if err != nil {
		if errors.Is(err, billing.ErrNotImplemented) {
			r.supported.WithLabelValues(r.ProviderName).Set(0)
			return &reconcileError{reason: "unsupported", err: fmt.Errorf("provider reconcile: %w", err)}
		}
		return &reconcileError{reason: "provider", err: fmt.Errorf("provider reconcile: %w", err)}
	}
	drift := delivered - pushed
	r.emit(acct.ID, delivered, pushed, drift)
	return nil
}

// stampsUsageAtDelivery reports whether the provider totals usage by the time
// meterd sent it (Polar, ADR-20261028101500).
func (r *Reconciler) stampsUsageAtDelivery() bool {
	stamper, ok := r.Provider.(billing.DeliveryTimeStamper)
	return ok && stamper.StampsUsageAtDelivery()
}

// reconcileDelivered compares what meterd delivered in the window, from its
// delivery ledger, with what the provider counted in it. Both are bucketed by
// delivery time. Comparing the provider's total with local usage by the hour
// it was used showed the delivery lag, a new account's first day and every
// meterd catch-up as several percent of drift (bug hunt 2026-10-28 B22).
// Usage meterd has not delivered yet is the delivery alerts' concern
// (FaasMeterdUsageDeliveryStalled, FaasMeterdBillingPushFailing).
func (r *Reconciler) reconcileDelivered(ctx context.Context, acct state.Account, start, end time.Time) error {
	ledger, ok := r.Store.(state.BillingUsageDeliveredSummer)
	if !ok {
		return &reconcileError{reason: "store", err: errors.New("store cannot sum delivered usage")}
	}
	local, err := ledger.SumBillingUsageDelivered(ctx, r.ProviderName, acct.ID, start, end)
	if err != nil {
		return &reconcileError{reason: "usage", err: fmt.Errorf("delivered usage: %w", err)}
	}
	pushed, err := r.Provider.ReconcileUsage(ctx, acct, start, end)
	if err != nil {
		if errors.Is(err, billing.ErrNotImplemented) {
			r.supported.WithLabelValues(r.ProviderName).Set(0)
			return &reconcileError{reason: "unsupported", err: fmt.Errorf("provider reconcile: %w", err)}
		}
		return &reconcileError{reason: "provider", err: fmt.Errorf("provider reconcile: %w", err)}
	}
	r.emit(acct.ID, local, pushed, local-pushed)
	return nil
}

func (r *Reconciler) emit(accountID string, local, pushed, drift int64) {
	labels := prometheus.Labels{
		"account_id": accountID,
		"provider":   r.ProviderName,
	}
	r.driftMBSeconds.With(labels).Set(float64(drift))
	maxv := local
	if pushed > maxv {
		maxv = pushed
	}
	var ratio float64
	if maxv > 0 {
		ratio = float64(abs(drift)) / float64(maxv)
	}
	r.driftRatio.With(labels).Set(ratio)
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// Loop is a free-function goroutine that calls RunOnce every
// Interval until ctx is cancelled. Matches the cadence contract
// for the other meterd ticks (pkg/meter/loop.go::Run).
//
// Returns when ctx.Done() fires. Caller is responsible for
// goroutine lifecycle (cmd/meterd/main.go launches this in a
// `go` statement at boot).
func (r *Reconciler) Loop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.RunOnce(ctx); err != nil {
				if r.Log != nil {
					r.Log.Error("reconcile tick failed", "err", err)
				}
			}
		}
	}
}
