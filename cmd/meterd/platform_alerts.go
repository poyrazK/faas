package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/platformalerts"
	"github.com/onebox-faas/faas/pkg/state"
)

// startPlatformAlerts emails accounts about outages and paused failing crons
// that no alert rule of theirs covers (ADR-20261115194000). Without a mail
// transport the notices wait; without Prometheus only cron notices send.
func startPlatformAlerts(ctx context.Context, store any, m mail.Sender, prom any, reg prometheus.Registerer, log *slog.Logger, dashboardURL string, release *observeRelease) {
	alertStore, ok := store.(platformalerts.Store)
	if !ok || m == nil {
		log.Info("meterd: platform alerts off", "store", ok, "mail", m != nil)
		return
	}
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meterd_platform_alert_notices_total",
		Help: "Platform alert emails (app_outage, cron_failing_paused, check_failing, slo_burn) by outcome: sent, skipped (app gone or no verified recipient), retry or failed. ADR-20261115194000.",
	}, []string{"kind", "outcome"})
	for _, kind := range []string{string(state.PlatformAlertAppOutage), string(state.PlatformAlertCronFailingPaused), string(state.PlatformAlertCheckFailing), string(state.PlatformAlertSLOBurn)} {
		for _, o := range []string{platformalerts.OutcomeSent, platformalerts.OutcomeSkipped, platformalerts.OutcomeRetry, platformalerts.OutcomeFailed} {
			c.WithLabelValues(kind, o).Add(0)
		}
	}
	if reg != nil {
		reg.MustRegister(c)
	}
	w := &platformalerts.Worker{
		Store:   alertStore,
		Mail:    meterdChannelMailer{m},
		Log:     log,
		Outcome: func(kind, outcome string) { c.WithLabelValues(kind, outcome).Inc() },
		// The links and gates meterd's own alert work uses.
		DashboardURL: dashboardURL,
	}
	if release != nil {
		w.CheckReleased, w.SLOReleased = release.Synthetic, release.SLO
	}
	if metrics, ok := prom.(platformalerts.Metrics); ok && metrics != nil {
		w.Metrics = metrics
	}
	go w.Run(ctx)
}

// dashboardBaseURL is the origin customer notifications link to: the platform
// domain that serves the dashboard (ADR-20261026100000), else the apps domain
// of the shared layout.
func dashboardBaseURL(getenv func(string) string, appsDomain string) string {
	domain := strings.TrimSpace(getenv("FAAS_PLATFORM_DOMAIN"))
	if domain == "" {
		domain = appsDomain
	}
	if domain == "" {
		return ""
	}
	return "https://" + domain
}
