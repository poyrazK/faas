package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type environmentGitSourceHealthStub struct {
	health           state.EnvironmentGitSourceHealth
	err              error
	calls            int
	waitForCancel    bool
	observedDeadline time.Duration
}

func (s *environmentGitSourceHealthStub) EnvironmentGitSourceHealth(ctx context.Context, _ time.Time, staleAfter time.Duration) (state.EnvironmentGitSourceHealth, error) {
	s.calls++
	if staleAfter != api.EnvironmentGitSourceStaleAfter {
		return state.EnvironmentGitSourceHealth{}, state.ErrInvalidArgument
	}
	deadline, _ := ctx.Deadline()
	s.observedDeadline = time.Until(deadline)
	if s.waitForCancel {
		<-ctx.Done()
		return state.EnvironmentGitSourceHealth{}, ctx.Err()
	}
	return s.health, s.err
}

func TestEnvironmentGitSourceMetricsExposeDurableHealthWithoutCustomerLabels(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	store := &environmentGitSourceHealthStub{health: state.EnvironmentGitSourceHealth{
		Active: 3, Suspended: 1, Unchecked: 1, Unverified: 1, PollStale: 1, VerificationStale: 2,
		Unavailable: 1, CandidatePendingApproval: 1, ApprovedPendingApply: 2, OldestCheckAgeSeconds: 601, OldestVerificationAgeSeconds: 902,
	}}
	enabled := &atomic.Bool{}
	enabled.Store(true)
	metrics := newEnvironmentGitSourceMetrics(t.Context(), registry, "apid", store, enabled)
	if duplicate := newEnvironmentGitSourceMetrics(t.Context(), registry, "apid", store, enabled); duplicate != metrics || store.calls != 0 {
		t.Fatal("collector registration duplicated metrics or read the database")
	}
	if err := testutil.GatherAndCompare(registry, strings.NewReader(`
# HELP apid_environment_git_source_health_up 1 when durable environment Git source health was read successfully during this scrape.
# TYPE apid_environment_git_source_health_up gauge
apid_environment_git_source_health_up 1
# HELP apid_environment_git_source_polling_enabled 1 when environment Git source polling is configured on this apid replica.
# TYPE apid_environment_git_source_polling_enabled gauge
apid_environment_git_source_polling_enabled 1
# HELP apid_environment_git_sources Environment Git sources by overlapping bounded condition; suspended sources contribute only to suspended.
# TYPE apid_environment_git_sources gauge
apid_environment_git_sources{condition="active"} 3
apid_environment_git_sources{condition="suspended"} 1
apid_environment_git_sources{condition="unchecked"} 1
apid_environment_git_sources{condition="unverified"} 1
apid_environment_git_sources{condition="poll_stale"} 1
apid_environment_git_sources{condition="verification_stale"} 2
apid_environment_git_sources{condition="unavailable"} 1
apid_environment_git_sources{condition="candidate_pending_approval"} 1
apid_environment_git_sources{condition="approved_pending_apply"} 2
# HELP apid_environment_git_source_oldest_check_age_seconds Oldest active source check age; a source never checked uses its creation time.
# TYPE apid_environment_git_source_oldest_check_age_seconds gauge
apid_environment_git_source_oldest_check_age_seconds 601
# HELP apid_environment_git_source_oldest_verification_age_seconds Oldest active source successful verification age; a source never verified uses its creation time.
# TYPE apid_environment_git_source_oldest_verification_age_seconds gauge
apid_environment_git_source_oldest_verification_age_seconds 902
`)); err != nil {
		t.Fatal(err)
	}
	if store.observedDeadline <= 0 || store.observedDeadline > api.EnvironmentGitSourceHealthTimeout {
		t.Fatalf("health query did not receive the bounded deadline: %v", store.observedDeadline)
	}
	// A previously successful observation must disappear on the next failed
	// scrape. Neither stale counts nor invented healthy zeroes are retained.
	store.err = errors.New("repository credential or customer definition")
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 2 {
		t.Fatalf("unavailable evidence retained counts or ages: %v", families)
	}
	for _, family := range families {
		if family.GetName() == "apid_environment_git_source_health_up" && family.Metric[0].Gauge.GetValue() != 0 {
			t.Fatal("database failure became healthy")
		}
	}
	store.err = nil
	enabled.Store(false)
	if families, err = registry.Gather(); err != nil || len(families) != 5 {
		t.Fatalf("health did not recover while polling was explicitly disabled: %v %v", families, err)
	}
	for _, family := range families {
		if family.GetName() == "apid_environment_git_source_polling_enabled" && family.Metric[0].Gauge.GetValue() != 0 {
			t.Fatal("disabled polling was still advertised as enabled")
		}
	}
}

func TestEnvironmentGitSourceMetricsBoundDatabaseWaitAndHonorShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "shutdown"}[shutdown], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if shutdown {
				cancel()
			}
			registry := prometheus.NewRegistry()
			store := &environmentGitSourceHealthStub{waitForCancel: true}
			newEnvironmentGitSourceMetrics(ctx, registry, "apid", store, &atomic.Bool{})
			started := time.Now()
			families, err := registry.Gather()
			if err != nil || len(families) != 2 {
				t.Fatalf("unavailable scrape: %v %v", families, err)
			}
			if elapsed := time.Since(started); elapsed > api.EnvironmentGitSourceHealthTimeout+time.Second {
				t.Fatalf("database health read blocked beyond its deadline: %v", elapsed)
			}
		})
	}
}

func TestEnvironmentGitSourceMetricsWireTheDaemonRegistry(t *testing.T) {
	srv := &server{store: state.NewMemStore()}
	ops := wire.NewOpsMetrics("apid")
	srv.WithOpsMetrics(t.Context(), ops)
	metrics := srv.environmentGitSourceMetrics
	srv.WithOpsMetrics(t.Context(), ops)
	if metrics == nil || srv.environmentGitSourceMetrics != metrics {
		t.Fatal("repeated wiring duplicated the health collector")
	}
	srv.startEnvironmentGitSourcePolling(t.Context(), func(string) string { return "false" })
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, family := range families {
		if strings.HasPrefix(family.GetName(), "apid_environment_git_") {
			seen[family.GetName()] = true
			if family.GetName() == "apid_environment_git_source_polling_enabled" && family.Metric[0].Gauge.GetValue() != 0 {
				t.Fatal("explicit disable was lost during daemon startup")
			}
		}
	}
	if len(seen) != 5 {
		t.Fatalf("GitOps health is absent from the daemon registry: %v", seen)
	}
	srv.WithOpsMetrics(t.Context(), nil)
	if srv.environmentGitSourceMetrics != nil {
		t.Fatal("nil metrics wiring retained its observer")
	}
}
