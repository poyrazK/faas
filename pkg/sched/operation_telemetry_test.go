// adr: 20261016123221
package sched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type telemetryStore struct {
	snapshot state.OperationTelemetry
	err      error
}

func (s *telemetryStore) OperationTelemetry(context.Context, time.Time) (state.OperationTelemetry, error) {
	return s.snapshot, s.err
}

func TestOperationTelemetryCollectsOneBoundedSnapshot(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &telemetryStore{snapshot: state.OperationTelemetry{
		ObservedAt: now,
		States: map[api.OperationState]state.OperationStateTelemetry{
			api.OperationWaiting:                {Count: 2, OldestUpdate: now.Add(-90 * time.Second)},
			api.OperationRequiresReconciliation: {Count: 1, OldestUpdate: now.Add(-time.Hour)},
		},
		Waiting: map[string]int64{api.OperationWaitApproval: 2},
		Overdue: 1,
		Outcomes: map[state.OperationOutcomeKey]int64{
			{EventType: "succeeded"}: 4,
			{EventType: "failed", FailureCode: api.OperationFailureBudgetExhausted}: 1,
			{EventType: "failed", FailureCode: "made_up_by_a_future_release"}:       2,
			{EventType: "failed", FailureCode: "another_new_code"}:                  1,
			{EventType: "milestone"}: 9,
		},
		OutcomeUnknownAccounts: 3,
	}}
	reg := prometheus.NewRegistry()
	telemetry := NewOperationTelemetry(reg, "schedd", store)
	if n, err := testutil.GatherAndCount(reg); err != nil || n != 0 {
		t.Fatalf("collected %d series before the first read: %v", n, err)
	}
	if err := telemetry.Refresh(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	want := `
# HELP schedd_customer_operations Customer Operations accepted, running, waiting or requiring reconciliation, fleet-wide.
# TYPE schedd_customer_operations gauge
schedd_customer_operations{state="accepted"} 0
schedd_customer_operations{state="requires_reconciliation"} 1
schedd_customer_operations{state="running"} 0
schedd_customer_operations{state="waiting"} 2
# HELP schedd_customer_operations_oldest_update_seconds Time since the least recently changed Customer Operation in a state changed; 0 when none.
# TYPE schedd_customer_operations_oldest_update_seconds gauge
schedd_customer_operations_oldest_update_seconds{state="accepted"} 0
schedd_customer_operations_oldest_update_seconds{state="requires_reconciliation"} 3600
schedd_customer_operations_oldest_update_seconds{state="running"} 0
schedd_customer_operations_oldest_update_seconds{state="waiting"} 90
# HELP schedd_customer_operations_settled_recent Customer Operation outcomes in the last 15 minutes by failure code.
# TYPE schedd_customer_operations_settled_recent gauge
schedd_customer_operations_settled_recent{failure_code="budget_exhausted",outcome="failed"} 1
schedd_customer_operations_settled_recent{failure_code="none",outcome="succeeded"} 4
schedd_customer_operations_settled_recent{failure_code="other",outcome="failed"} 3
# HELP schedd_customer_operations_waiting Waiting Customer Operations by wait kind, fleet-wide.
# TYPE schedd_customer_operations_waiting gauge
schedd_customer_operations_waiting{kind="approval"} 2
schedd_customer_operations_waiting{kind="event"} 0
schedd_customer_operations_waiting{kind="timer"} 0
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "schedd_customer_operations", "schedd_customer_operations_oldest_update_seconds",
		"schedd_customer_operations_settled_recent", "schedd_customer_operations_waiting"); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]float64{
		"schedd_customer_operations_overdue": 1, "schedd_customer_operations_outcome_unknown_accounts": 3,
		"schedd_customer_operations_telemetry_refreshed_timestamp_seconds": float64(now.Unix()),
	} {
		if got := gaugeValue(t, reg, name); got != value {
			t.Errorf("%s = %v, want %v", name, got, value)
		}
	}

	// A failed read keeps the last snapshot rather than dropping the series.
	store.err = errors.New("postgres unavailable")
	if err := telemetry.Refresh(context.Background(), now.Add(time.Minute)); err == nil {
		t.Fatal("a failed read was reported as refreshed")
	}
	if got := gaugeValue(t, reg, "schedd_customer_operations_telemetry_refreshed_timestamp_seconds"); got != float64(now.Unix()) {
		t.Fatalf("refresh timestamp moved to %v after a failed read", got)
	}
}

func TestOperationTelemetryNeedsAnOperationStore(t *testing.T) {
	if NewOperationTelemetry(prometheus.NewRegistry(), "schedd", nil) != nil {
		t.Fatal("telemetry without a store")
	}
	var none *OperationTelemetry
	if err := none.Refresh(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func gaugeValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name && len(family.GetMetric()) == 1 {
			return family.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("%s not gathered", name)
	return 0
}
