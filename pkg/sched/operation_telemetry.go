// adr: 20261016123221
package sched

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// operationTelemetryStates are the pending states the gauges always report,
// zero included, so a rule never reads an absent series as healthy.
var operationTelemetryStates = []api.OperationState{api.OperationAccepted, api.OperationRunning, api.OperationWaiting, api.OperationRequiresReconciliation}

var operationTelemetryWaitKinds = []string{api.OperationWaitTimer, api.OperationWaitApproval, api.OperationWaitEvent}

// operationTelemetryFailureCodes bounds the failure_code label; anything else
// is reported as "other".
var operationTelemetryFailureCodes = map[string]bool{
	"execution_failed": true, "execution_outcome_unknown": true, "invalid_output": true, "job_result_missing": true,
	api.OperationFailureDeadlineExceeded: true, api.OperationFailureBudgetExhausted: true,
	api.OperationFailureApprovalDenied: true, api.OperationFailureApprovalExpiry: true, api.OperationFailureEventExpiry: true,
}

// operationTelemetryOutcomes names the outcome events by the state they lead to.
var operationTelemetryOutcomes = map[string]string{
	"succeeded": "succeeded", "failed": "failed", "cancelled": "cancelled", "reconciliation_required": "requires_reconciliation",
}

// OperationTelemetry serves fleet-wide Customer Operations gauges from the
// snapshot the retention tick last read. A scrape sees one whole snapshot
// and no series from an earlier one.
type OperationTelemetry struct {
	store    state.OperationTelemetryStore
	snapshot atomic.Pointer[state.OperationTelemetry]

	operations, waiting, oldest, overdue, settled, unknownAccounts, refreshed *prometheus.Desc
}

// NewOperationTelemetry registers the collector, or returns nil without a
// store that can read Operations.
func NewOperationTelemetry(reg prometheus.Registerer, prefix string, store state.OperationTelemetryStore) *OperationTelemetry {
	if store == nil {
		return nil
	}
	t := &OperationTelemetry{store: store,
		operations: prometheus.NewDesc(prefix+"_customer_operations", "Customer Operations accepted, running, waiting or requiring reconciliation, fleet-wide.", []string{"state"}, nil),
		waiting:    prometheus.NewDesc(prefix+"_customer_operations_waiting", "Waiting Customer Operations by wait kind, fleet-wide.", []string{"kind"}, nil),
		oldest:     prometheus.NewDesc(prefix+"_customer_operations_oldest_update_seconds", "Time since the least recently changed Customer Operation in a state changed; 0 when none.", []string{"state"}, nil),
		overdue:    prometheus.NewDesc(prefix+"_customer_operations_overdue", "Active Customer Operations more than three deadline sweeps past their deadline.", nil, nil),
		settled:    prometheus.NewDesc(prefix+"_customer_operations_settled_recent", "Customer Operation outcomes in the last 15 minutes by failure code.", []string{"outcome", "failure_code"}, nil),
		unknownAccounts: prometheus.NewDesc(prefix+"_customer_operations_outcome_unknown_accounts",
			"Accounts with an attempt that ended with an unknown outcome in the last 15 minutes.", nil, nil),
		refreshed: prometheus.NewDesc(prefix+"_customer_operations_telemetry_refreshed_timestamp_seconds", "When the Customer Operations gauges were last read from Postgres.", nil, nil),
	}
	if reg != nil {
		reg.MustRegister(t)
	}
	return t
}

// Refresh reads a new snapshot; a failed read keeps the previous one.
func (t *OperationTelemetry) Refresh(ctx context.Context, now time.Time) error {
	if t == nil {
		return nil
	}
	snapshot, err := t.store.OperationTelemetry(ctx, now)
	if err != nil {
		return err
	}
	t.snapshot.Store(&snapshot)
	return nil
}

func (t *OperationTelemetry) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{t.operations, t.waiting, t.oldest, t.overdue, t.settled, t.unknownAccounts, t.refreshed} {
		ch <- desc
	}
}

func (t *OperationTelemetry) Collect(ch chan<- prometheus.Metric) {
	snapshot := t.snapshot.Load()
	if snapshot == nil {
		return
	}
	for _, s := range operationTelemetryStates {
		entry := snapshot.States[s]
		age := 0.0
		if entry.Count > 0 {
			age = max(snapshot.ObservedAt.Sub(entry.OldestUpdate).Seconds(), 0)
		}
		ch <- prometheus.MustNewConstMetric(t.operations, prometheus.GaugeValue, float64(entry.Count), string(s))
		ch <- prometheus.MustNewConstMetric(t.oldest, prometheus.GaugeValue, age, string(s))
	}
	for _, kind := range operationTelemetryWaitKinds {
		ch <- prometheus.MustNewConstMetric(t.waiting, prometheus.GaugeValue, float64(snapshot.Waiting[kind]), kind)
	}
	ch <- prometheus.MustNewConstMetric(t.overdue, prometheus.GaugeValue, float64(snapshot.Overdue))
	settled := map[[2]string]int64{}
	for key, n := range snapshot.Outcomes {
		outcome, ok := operationTelemetryOutcomes[key.EventType]
		if !ok {
			continue
		}
		code := key.FailureCode
		switch {
		case code == "":
			code = "none"
		case !operationTelemetryFailureCodes[code]:
			code = "other"
		}
		settled[[2]string{outcome, code}] += n
	}
	for key, n := range settled {
		ch <- prometheus.MustNewConstMetric(t.settled, prometheus.GaugeValue, float64(n), key[0], key[1])
	}
	ch <- prometheus.MustNewConstMetric(t.unknownAccounts, prometheus.GaugeValue, float64(snapshot.OutcomeUnknownAccounts))
	ch <- prometheus.MustNewConstMetric(t.refreshed, prometheus.GaugeValue, float64(snapshot.ObservedAt.Unix()))
}
