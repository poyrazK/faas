package internalmail

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type schedulingStatisticsFunc func(context.Context) (SchedulingStatistics, error)

func (f schedulingStatisticsFunc) SchedulingStatistics(ctx context.Context) (SchedulingStatistics, error) {
	return f(ctx)
}

func TestSchedulingMonitorDefaultOffAndFailurePrivacy(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewSchedulingMonitor(schedulingStatisticsFunc(func(context.Context) (SchedulingStatistics, error) {
		calls++
		if failed.Load() {
			return SchedulingStatistics{}, errors.New("private message diagnostic")
		}
		return SchedulingStatistics{ScheduledWaiting: 2, ExpiringPending: 3}, nil
	}), enabled.Load))
	body := scrapeMonitor(t, registry)
	if calls != 0 || !strings.Contains(body, "maild_mail_scheduling_enabled 0") || strings.Contains(body, "scheduled_waiting") {
		t.Fatal("disabled scheduling monitor read state")
	}
	enabled.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_scheduling_stats_up 1") || !strings.Contains(body, "maild_mail_scheduling_scheduled_waiting 2") || !strings.Contains(body, "maild_mail_scheduling_expiring_pending 3") {
		t.Fatal("scheduling inventory not visible")
	}
	failed.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_scheduling_stats_up 0") || strings.Contains(body, "private") || strings.Contains(body, "scheduled_waiting") {
		t.Fatal("failed scheduling collection leaked or retained healthy inventory")
	}
}
