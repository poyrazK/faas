package internalmail

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type pacingStatisticsFunc func(context.Context) (PacingStatistics, error)

func (f pacingStatisticsFunc) PacingStatistics(ctx context.Context) (PacingStatistics, error) {
	return f(ctx)
}

func TestDomainPacingMonitorDefaultOffAndFailurePrivacy(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewPacingMonitor(pacingStatisticsFunc(func(context.Context) (PacingStatistics, error) {
		calls++
		if failed.Load() {
			return PacingStatistics{}, errors.New("secret recipient domain diagnostic")
		}
		return PacingStatistics{CoolingDomains: 2, WaitingRecipients: 3}, nil
	}), enabled.Load))
	body := scrapeMonitor(t, registry)
	if calls != 0 || !strings.Contains(body, "maild_mail_domain_pacing_enabled 0") || strings.Contains(body, "waiting_recipients") {
		t.Fatal("disabled pacing monitor read state")
	}
	enabled.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_domain_pacing_stats_up 1") || !strings.Contains(body, "maild_mail_domain_pacing_cooling_domains 2") || !strings.Contains(body, "maild_mail_domain_pacing_waiting_recipients 3") {
		t.Fatal("pacing aggregate inventory missing")
	}
	failed.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_domain_pacing_stats_up 0") || strings.Contains(body, "secret") || strings.Contains(body, "waiting_recipients") {
		t.Fatal("failed pacing monitor leaked diagnostics or retained healthy inventory")
	}
}
