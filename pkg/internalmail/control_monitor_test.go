package internalmail

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type controlStatisticsFunc func(context.Context) (map[string]int64, error)

func (f controlStatisticsFunc) DeliveryControlStatistics(ctx context.Context) (map[string]int64, error) {
	return f(ctx)
}

func TestDeliveryControlMonitorDefaultOffFailureAndPrivacy(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewDeliveryControlMonitor(controlStatisticsFunc(func(context.Context) (map[string]int64, error) {
		calls++
		if failed.Load() {
			return nil, errors.New("private domain diagnostic")
		}
		return map[string]int64{"paused": 2, "draining": 1, "private-domain.test": 3}, nil
	}), enabled.Load))
	body := scrapeMonitor(t, registry)
	if calls != 0 || !strings.Contains(body, "maild_mail_controls_enabled 0") || strings.Contains(body, "controls_paused_scopes") {
		t.Fatal("disabled control metrics accessed state")
	}
	enabled.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_controls_stats_up 1") || !strings.Contains(body, "maild_mail_controls_paused_scopes 2") || !strings.Contains(body, "maild_mail_controls_draining_scopes 1") || strings.Contains(body, "private") {
		t.Fatal("control metrics lost aggregates or exposed identifiers")
	}
	failed.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_controls_stats_up 0") || strings.Contains(body, "controls_paused_scopes") || strings.Contains(body, "private") {
		t.Fatal("failed control collection looked healthy or exposed diagnostics")
	}
}
