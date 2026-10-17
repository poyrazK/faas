package internalmail

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type lifecycleStatisticsFunc func(context.Context, string) (DomainLifecycleStatistics, error)

func (f lifecycleStatisticsFunc) DomainLifecycleStatistics(ctx context.Context, ip string) (DomainLifecycleStatistics, error) {
	return f(ctx, ip)
}

func TestDomainLifecycleMonitorDefaultOffFailureAndPrivacy(t *testing.T) {
	var rotation, reverify, failed atomic.Bool
	calls := 0
	m := NewDomainLifecycleMonitor(lifecycleStatisticsFunc(func(context.Context, string) (DomainLifecycleStatistics, error) {
		calls++
		if failed.Load() {
			return DomainLifecycleStatistics{}, errors.New("private signing domain diagnostic")
		}
		return DomainLifecycleStatistics{Paused: 1, Stale: 2, Unavailable: 3, Staged: 4, FailedKeys: 1, OldestStagedSeconds: 86401}, nil
	}), rotation.Load, reverify.Load, "192.0.2.10")
	registry := prometheus.NewRegistry()
	registry.MustRegister(m)
	text := scrapeMonitor(t, registry)
	if calls != 0 || !strings.Contains(text, "maild_mail_dkim_rotation_enabled 0") || !strings.Contains(text, "maild_mail_domain_reverify_enabled 0") || strings.Contains(text, "domain_stale") {
		t.Fatal("disabled metrics read state")
	}
	rotation.Store(true)
	reverify.Store(true)
	m.Observe(nil)
	text = scrapeMonitor(t, registry)
	for _, want := range []string{"maild_mail_dkim_failed_keys 1", "maild_mail_domain_paused 1", "maild_mail_domain_stale 2", "maild_mail_domain_check_last_run_up 1", "maild_mail_domain_lifecycle_stats_up 1"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing durable aggregate", want)
		}
	}
	failed.Store(true)
	m.Observe(errors.New("private resolver diagnostic"))
	text = scrapeMonitor(t, registry)
	if strings.Contains(text, "private") || strings.Contains(text, "domain_stale") || !strings.Contains(text, "maild_mail_domain_lifecycle_stats_up 0") || !strings.Contains(text, "maild_mail_domain_check_last_run_up 0") {
		t.Fatal("collection failure leaked or looked healthy")
	}
}
