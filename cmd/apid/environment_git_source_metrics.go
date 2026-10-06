package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// Only aggregate counts and closed-set conditions are exposed. Repository,
// source, account, commit, definition, and error contents never become labels.
// Freshness is read from durable state on each scrape, independently of which
// apid replica owns a poll lease.
type environmentGitSourceMetrics struct {
	registry *prometheus.Registry
	ctx      context.Context
	store    state.EnvironmentGitSourceHealthStore
	enabled  *atomic.Bool
	now      func() time.Time

	healthUp, pollingEnabled, sources, oldestCheck, oldestVerification *prometheus.Desc
}

func newEnvironmentGitSourceMetrics(ctx context.Context, registry *prometheus.Registry, prefix string, store state.EnvironmentGitSourceHealthStore, enabled *atomic.Bool) *environmentGitSourceMetrics {
	m := &environmentGitSourceMetrics{registry: registry, ctx: ctx, store: store, enabled: enabled, now: time.Now,
		healthUp:           prometheus.NewDesc(prefix+"_environment_git_source_health_up", "1 when durable environment Git source health was read successfully during this scrape.", nil, nil),
		pollingEnabled:     prometheus.NewDesc(prefix+"_environment_git_source_polling_enabled", "1 when environment Git source polling is configured on this apid replica.", nil, nil),
		sources:            prometheus.NewDesc(prefix+"_environment_git_sources", "Environment Git sources by overlapping bounded condition; suspended sources contribute only to suspended.", []string{"condition"}, nil),
		oldestCheck:        prometheus.NewDesc(prefix+"_environment_git_source_oldest_check_age_seconds", "Oldest active source check age; a source never checked uses its creation time.", nil, nil),
		oldestVerification: prometheus.NewDesc(prefix+"_environment_git_source_oldest_verification_age_seconds", "Oldest active source successful verification age; a source never verified uses its creation time.", nil, nil),
	}
	if err := registry.Register(m); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			return duplicate.ExistingCollector.(*environmentGitSourceMetrics)
		}
		panic(err)
	}
	return m
}

func (m *environmentGitSourceMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{m.healthUp, m.pollingEnabled, m.sources, m.oldestCheck, m.oldestVerification} {
		ch <- desc
	}
}

func (m *environmentGitSourceMetrics) Collect(ch chan<- prometheus.Metric) {
	enabled := 0.0
	if m.enabled != nil && m.enabled.Load() {
		enabled = 1
	}
	ch <- prometheus.MustNewConstMetric(m.pollingEnabled, prometheus.GaugeValue, enabled)
	ctx, cancel := context.WithTimeout(m.ctx, api.EnvironmentGitSourceHealthTimeout)
	defer cancel()
	var health state.EnvironmentGitSourceHealth
	err := state.ErrNotFound
	if m.store != nil {
		health, err = m.store.EnvironmentGitSourceHealth(ctx, m.now().UTC(), api.EnvironmentGitSourceStaleAfter)
	}
	if err != nil {
		ch <- prometheus.MustNewConstMetric(m.healthUp, prometheus.GaugeValue, 0)
		return // Never replace unavailable evidence with healthy zero counts.
	}
	ch <- prometheus.MustNewConstMetric(m.healthUp, prometheus.GaugeValue, 1)
	for _, condition := range []struct {
		name  string
		count int64
	}{
		{"active", health.Active}, {"suspended", health.Suspended}, {"unchecked", health.Unchecked},
		{"unverified", health.Unverified}, {"poll_stale", health.PollStale}, {"verification_stale", health.VerificationStale},
		{"unavailable", health.Unavailable}, {"candidate_pending_approval", health.CandidatePendingApproval},
		{"approved_pending_apply", health.ApprovedPendingApply},
	} {
		ch <- prometheus.MustNewConstMetric(m.sources, prometheus.GaugeValue, float64(condition.count), condition.name)
	}
	ch <- prometheus.MustNewConstMetric(m.oldestCheck, prometheus.GaugeValue, health.OldestCheckAgeSeconds)
	ch <- prometheus.MustNewConstMetric(m.oldestVerification, prometheus.GaugeValue, health.OldestVerificationAgeSeconds)
}
