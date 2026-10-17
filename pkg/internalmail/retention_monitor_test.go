package internalmail

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type retentionStatisticsFunc func(context.Context, RetentionPolicy) (RetentionStatistics, error)

func (f retentionStatisticsFunc) RetentionStatistics(ctx context.Context, policy RetentionPolicy) (RetentionStatistics, error) {
	return f(ctx, policy)
}

func TestRetentionMonitorDefaultOffPrivacyAndFailureVisibility(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	monitor := NewRetentionMonitor(retentionStatisticsFunc(func(context.Context, RetentionPolicy) (RetentionStatistics, error) {
		calls++
		if failed.Load() {
			return RetentionStatistics{}, errors.New("private database diagnostic")
		}
		return RetentionStatistics{RetainedMessages: 3, PrunedMessages: 2, RetainedBytes: 1000, EligibleMessages: 1, EligibleBytes: 100}, nil
	}), enabled.Load, DefaultRetentionPolicy())
	registry := prometheus.NewRegistry()
	registry.MustRegister(monitor)
	if text := scrapeMonitor(t, registry); calls != 0 || !strings.Contains(text, "maild_mail_retention_enabled 0") || strings.Contains(text, "retention_retained_messages") {
		t.Fatal("disabled retention metrics accessed state", text)
	}
	enabled.Store(true)
	monitor.Observe(RetentionResult{PrunedMessages: 2, PrunedBytes: 500}, nil)
	text := scrapeMonitor(t, registry)
	for _, expected := range []string{"maild_mail_retention_enabled 1", "maild_mail_retention_dry_run 1", "maild_mail_retention_stats_up 1", "maild_mail_retention_last_run_up 1", "maild_mail_retention_retained_messages 3", "maild_mail_retention_last_pruned_messages 2"} {
		if !strings.Contains(text, expected) {
			t.Fatal("missing aggregate retention metric", expected)
		}
	}
	failed.Store(true)
	monitor.Observe(RetentionResult{}, errors.New("private payload failure"))
	text = scrapeMonitor(t, registry)
	if strings.Contains(text, "private") || strings.Contains(text, "retention_retained_messages") || !strings.Contains(text, "maild_mail_retention_stats_up 0") || !strings.Contains(text, "maild_mail_retention_last_run_up 0") {
		t.Fatal("retention failure looked healthy or leaked diagnostics", text)
	}
}
