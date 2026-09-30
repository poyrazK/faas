// adr: 375
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type refusingTrafficDomainStore struct {
	*state.MemStore
	refusal error
	stale   bool
	calls   int
}

func (s *refusingTrafficDomainStore) MarkDomainVerifiedIfChallenge(ctx context.Context, domain, token string) (bool, error) {
	s.calls++
	if s.refusal != nil {
		return false, fmt.Errorf("domain publication: %w", s.refusal)
	}
	if s.stale {
		return false, nil
	}
	return s.MemStore.MarkDomainVerifiedIfChallenge(ctx, domain, token)
}

func TestTrafficDomainDNSPublicationRefusalAndRepair(t *testing.T) {
	for _, fixture := range []struct {
		name, outcome string
		refusal       error
		stale         bool
	}{
		{"bytes", "refused", &state.TrafficPolicyAggregateError{Scope: "host_rule_projection", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 1}, false},
		{"count", "refused", &state.TrafficPolicyAggregateError{Scope: "host_rule_count", Unit: "rules", Limit: api.TrafficPolicyMaxHostRules, Observed: api.TrafficPolicyMaxHostRules + 1}, false},
		{"analysis", "refused", &state.TrafficPolicyAnalysisError{Scope: "states", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: api.TrafficPolicyMaxAnalysisStates + 1}, false},
		{"store-error", "error", errors.New("publication store unavailable"), false},
		{"stale", "stale", nil, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
			deployment := mustSeedDeployment(t, e, "domain-activation")
			domain, err := e.store.CreateCustomDomain(t.Context(), "guarded.example.test", deployment.AppID, "token")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.UpdateCustomDomainCertStatus(t.Context(), domain.Domain, state.CustomDomainCertFailed, time.Now().Add(time.Hour), "previous failure", time.Now().Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			before, err := e.store.DomainByName(t.Context(), domain.Domain)
			if err != nil {
				t.Fatal(err)
			}
			refusing := &refusingTrafficDomainStore{MemStore: e.store, refusal: fixture.refusal, stale: fixture.stale}
			e.s.store = refusing
			e.s.domainVerificationMetrics = newDomainVerificationMetrics(prometheus.NewRegistry(), "apid_domain_test")
			previousLookup := txtLookupFunc
			t.Cleanup(func() { txtLookupFunc = previousLookup })
			txtLookupFunc = func(context.Context, string) ([]string, error) { return []string{"token"}, nil }
			notifier.mu.Lock()
			notificationsBefore := len(notifier.emitted)
			notifier.mu.Unlock()
			e.s.runVerifyOnce(t.Context(), slog.Default())
			if refusing.calls != 1 {
				t.Fatalf("verification calls=%d want=1", refusing.calls)
			}
			after, err := e.store.DomainByName(t.Context(), domain.Domain)
			if err != nil {
				t.Fatal(err)
			}
			// Claiming a due poll attempt advances its backoff independently
			// of publication. Verification/certificate intent must stay intact.
			after.VerificationNextCheckAt = before.VerificationNextCheckAt
			after.VerificationAttempts = before.VerificationAttempts
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("refused DNS publication changed intent: before=%+v after=%+v", before, after)
			}
			for _, outcome := range []string{"success", "refused", "stale", "error"} {
				want := float64(0)
				if outcome == fixture.outcome {
					want = 1
				}
				if got := testutil.ToFloat64(e.s.domainVerificationMetrics.publications.WithLabelValues(outcome)); got != want {
					t.Fatalf("publication outcome=%s got=%v want=%v", outcome, got, want)
				}
			}
			notifier.mu.Lock()
			unchanged := len(notifier.emitted) == notificationsBefore
			notifier.mu.Unlock()
			if !unchanged {
				t.Fatal("refused DNS publication requested certificate work")
			}
			refusing.refusal, refusing.stale = nil, false
			if err := e.store.RetryCustomDomainVerification(t.Context(), domain.Domain); err != nil {
				t.Fatal(err)
			}
			e.s.runVerifyOnce(t.Context(), slog.Default())
			published, err := e.store.DomainByName(t.Context(), domain.Domain)
			if err != nil || !published.Verified() || published.CertStatus != state.CustomDomainCertPending || published.CertLastError != "" || refusing.calls != 2 {
				t.Fatalf("DNS publication after repair: domain=%+v calls=%d err=%v", published, refusing.calls, err)
			}
			if got := testutil.ToFloat64(e.s.domainVerificationMetrics.publications.WithLabelValues("success")); got != 1 {
				t.Fatalf("successful publication metric=%v want=1", got)
			}
			notifier.mu.Lock()
			defer notifier.mu.Unlock()
			if len(notifier.emitted) != notificationsBefore+1 {
				t.Fatal("successful publication did not emit exactly one certificate notification")
			}
		})
	}
}
