package outbound

// adr: 1120 — a settlement is keyed by the call's admission lease and retried
// when an attempt fails, and admission reports the breaker and provider
// cooldown it read, so the call needs no second check.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordAIUsageRetriesOnlyKeyedTransientFailures(t *testing.T) {
	transient := errors.New("postgres: statement timeout")
	invalid := fmt.Errorf("%w: AI usage model is invalid", ErrInvalidIntegration)
	lease := uuid.NewString()
	for _, tc := range []struct {
		name     string
		leaseID  string
		failures []error
		attempts int
		retried  float64
		wantErr  bool
	}{
		{name: "first attempt succeeds", leaseID: lease, attempts: 1},
		{name: "a transient failure is retried", leaseID: lease, failures: []error{transient}, attempts: 2, retried: 1},
		{name: "the last attempt's failure is returned", leaseID: lease, failures: []error{transient, transient, transient},
			attempts: 3, retried: 2, wantErr: true},
		{name: "an invalid record is not retried", leaseID: lease, failures: []error{invalid}, attempts: 1, wantErr: true},
		{name: "a record without a key is not retried", failures: []error{transient}, attempts: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metrics, err := NewMetrics(prometheus.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			h := &Handler{Metrics: metrics}
			attempts := 0
			_, err = h.recordAIUsage(context.Background(), AIUsageRecord{LeaseID: tc.leaseID}, "integration-1",
				func(ctx context.Context) ([]AIBudgetAlert, error) {
					if _, ok := ctx.Deadline(); !ok {
						t.Error("a settlement attempt has no deadline")
					}
					attempts++
					if attempts <= len(tc.failures) {
						return nil, tc.failures[attempts-1]
					}
					return nil, nil
				})
			if attempts != tc.attempts || (err != nil) != tc.wantErr {
				t.Fatalf("attempts = %d, err = %v", attempts, err)
			}
			if retried := testutil.ToFloat64(metrics.aiUsageRecordEvents.WithLabelValues("integration-1", "retried")); retried != tc.retried {
				t.Fatalf("retried events = %v", retried)
			}
		})
	}
}

func TestMemoryAISettlementCountsALeaseOnce(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()
	decision, err := backend.Admit(ctx, AdmissionSpec{IntegrationID: "integration-1", RatePerSecond: 10, Burst: 10,
		MaxInFlight: 10, LeaseTTL: time.Minute, BindingAppID: "app-1"})
	if err != nil || !decision.Granted {
		t.Fatalf("admission = %+v, %v", decision, err)
	}
	record := AIUsageRecord{IntegrationID: "integration-1", Caller: "app-1", Outcome: AIUsageOutcomeKnown,
		Usage: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 20, OutputTokens: 10}, LeaseID: decision.LeaseID}
	for range 2 {
		if err := backend.RecordAIUsage(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	rollup, _ := backend.AIUsageRollup("integration-1", "app-1", "gpt-4o")
	if tokens := backend.DailyAITokenCount("integration-1"); tokens != 30 || rollup.Requests != 1 {
		t.Fatalf("a settled lease counted %d tokens in %d requests", tokens, rollup.Requests)
	}
	if _, live := backend.states["integration-1"].leases[decision.LeaseID]; live {
		t.Fatal("the settlement left the lease live")
	}
}

func TestMemoryAdmissionReportsTheBreakerAndCooldown(t *testing.T) {
	backend := NewMemoryBackend()
	testAdmissionReportsTheBreakerAndCooldown(t, backend, AdmissionSpec{IntegrationID: "integration-1",
		RatePerSecond: 100, Burst: 100, MaxInFlight: 100, LeaseTTL: time.Minute,
		CircuitBreakerFailureThreshold: 1, CircuitBreakerOpenSeconds: 30})
}

// gateBackend is a backend with a circuit breaker and a provider cooldown.
type gateBackend interface {
	Backend
	CircuitBreakerBackend
	ProviderCooldownBackend
}

func testAdmissionReportsTheBreakerAndCooldown(t *testing.T, backend gateBackend, spec AdmissionSpec) {
	t.Helper()
	ctx := context.Background()
	spec.PolicyRevision = 7
	admit := func() Decision {
		t.Helper()
		decision, err := backend.Admit(ctx, spec)
		if err != nil || !decision.Granted {
			t.Fatalf("admission = %+v, %v", decision, err)
		}
		return decision
	}
	first := admit()
	if !first.CircuitClosed || first.ProviderGate != nil {
		t.Fatalf("first admission = closed %v, provider gate %+v; want closed and no gate under a new revision",
			first.CircuitClosed, first.ProviderGate)
	}
	if _, err := backend.AllowProviderRequest(ctx, spec.IntegrationID, spec.PolicyRevision); err != nil {
		t.Fatal(err)
	}
	if second := admit(); second.ProviderGate == nil || !second.ProviderGate.Allowed {
		t.Fatalf("second admission's provider gate = %+v", second.ProviderGate)
	}
	if err := backend.RecordProviderCooldown(ctx, spec.IntegrationID, spec.PolicyRevision, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := backend.RecordCircuitOutcome(ctx, spec.IntegrationID, first.LeaseID, first.CircuitBreakerFailureThreshold,
		first.CircuitBreakerOpenSeconds, CircuitOutcomeFailure); err != nil {
		t.Fatal(err)
	}
	third := admit()
	if third.CircuitClosed || third.ProviderGate == nil || third.ProviderGate.Allowed || third.ProviderGate.RetryAfter <= 0 {
		t.Fatalf("admission after a failure and a cooldown = closed %v, provider gate %+v", third.CircuitClosed, third.ProviderGate)
	}
}

func TestCircuitOutcomeChangesNothing(t *testing.T) {
	lease := uuid.New()
	probe := pgtype.UUID{Bytes: lease, Valid: true}
	otherProbe := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	open := pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}
	for _, tc := range []struct {
		name      string
		failures  int
		openUntil pgtype.Timestamptz
		probe     pgtype.UUID
		stored    int
		outcome   CircuitBreakerOutcome
		want      bool
	}{
		{name: "success with no streak", stored: 5, outcome: CircuitOutcomeSuccess, want: true},
		{name: "success ends a streak", failures: 2, stored: 5, outcome: CircuitOutcomeSuccess},
		{name: "failure starts a streak", stored: 5, outcome: CircuitOutcomeFailure},
		{name: "neutral outcome", failures: 2, stored: 5, outcome: CircuitOutcomeNeutral, want: true},
		{name: "another call holds the breaker open", openUntil: open, probe: otherProbe, stored: 5, outcome: CircuitOutcomeFailure, want: true},
		{name: "the probe's outcome", openUntil: open, probe: probe, stored: 5, outcome: CircuitOutcomeSuccess},
		{name: "the probe's neutral outcome reopens", openUntil: open, probe: probe, stored: 5, outcome: CircuitOutcomeNeutral},
		{name: "a stale policy", failures: 2, stored: 3, outcome: CircuitOutcomeSuccess, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := circuitOutcomeChangesNothing(tc.failures, tc.openUntil, tc.probe, tc.stored, 30, 5, 30, lease, tc.outcome); got != tc.want {
				t.Fatalf("changes nothing = %v, want %v", got, tc.want)
			}
		})
	}
}
