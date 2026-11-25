package main

import (
	"context"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/agentapprovalnotify"
	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/savedinsightnotify"
)

// meterdChannelMailer adapts meterd's mail transport to alertchannels.
type meterdChannelMailer struct{ m mail.Sender }

func (a meterdChannelMailer) SendEmail(ctx context.Context, to, subject, body, key string) error {
	return a.m.Send(ctx, mail.Message{To: []string{to}, Subject: subject, TextBody: body, MessageID: key})
}

// newAlertChannelSender builds the ADR-749 notification sender. Without a
// mail transport, email channels report a delivery error instead of
// silently dropping alerts.
func newAlertChannelSender(m mail.Sender) *alertchannels.Sender {
	s := &alertchannels.Sender{HTTP: alertchannels.NewHTTPClient()}
	if m != nil {
		s.Mail = meterdChannelMailer{m}
	}
	return s
}

// newAlertChannelCounter registers meterd_alert_channel_deliveries_total and
// returns the evaluator's outcome callback (safe for concurrent use).
func newAlertChannelCounter(reg prometheus.Registerer) func(kind, outcome string) {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meterd_alert_channel_deliveries_total",
		Help: "Alert notification channel deliveries by channel kind (slack, pagerduty, email) and outcome (delivered, failed, unseal_failed). ADR-749.",
	}, []string{"kind", "outcome"})
	if reg != nil {
		reg.MustRegister(c)
	}
	return func(kind, outcome string) { c.WithLabelValues(kind, outcome).Inc() }
}

// newAlertRuleSkipCounter registers meterd_alert_rules_skipped_total and
// returns the evaluator's callback for rules a tick did not evaluate
// (ADR-20261028101615).
func newAlertRuleSkipCounter(reg prometheus.Registerer) func(reason string, n int) {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meterd_alert_rules_skipped_total",
		Help: "Enabled alert rules a tick did not evaluate, by reason: deferred (the account spent its tick budget; they run first next tick), not_released (the account's plan or capability flags no longer cover them) or over_plan_limit (past the plan's per-app or per-account rule cap, kept from a bigger plan). ADR-20261028101615, ADR-20261104142830.",
	}, []string{"reason"})
	for _, reason := range []string{"deferred", "not_released", "over_plan_limit"} {
		c.WithLabelValues(reason).Add(0)
	}
	if reg != nil {
		reg.MustRegister(c)
	}
	return func(reason string, n int) {
		if n > 0 {
			c.WithLabelValues(reason).Add(float64(n))
		}
	}
}

// startAgentApprovalNotifications emails the people who may decide agents'
// approval requests (ADR-20261107140000 §2, §5). Without a mail transport
// the rows wait; they are skipped once their requests expire.
func startAgentApprovalNotifications(ctx context.Context, store any, m mail.Sender, channels agentapprovalnotify.ChannelSender, reg prometheus.Registerer, log *slog.Logger) {
	notifyStore, ok := store.(agentapprovalnotify.Store)
	if !ok || m == nil {
		log.Info("meterd: agent approval notifications off", "store", ok, "mail", m != nil)
		return
	}
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meterd_agent_approval_notifications_total",
		Help: "Agent approval notifications by outcome: sent, deferred (the recipient was emailed about the key in the last 10 minutes), skipped (the request was decided, expired or has no recipient) or failed. ADR-20261107140000.",
	}, []string{"outcome"})
	for _, o := range []string{agentapprovalnotify.OutcomeSent, agentapprovalnotify.OutcomeDeferred, agentapprovalnotify.OutcomeSkipped, agentapprovalnotify.OutcomeFailed} {
		c.WithLabelValues(o).Add(0)
	}
	if reg != nil {
		reg.MustRegister(c)
	}
	n := &agentapprovalnotify.Notifier{
		Store: notifyStore, Mail: meterdChannelMailer{m}, Channels: channels, Log: log,
		Outcome: func(outcome string) { c.WithLabelValues(outcome).Inc() },
	}
	go n.Run(ctx)
}

// startSavedInsights runs saved insights' digests and threshold alerts
// (ADR-20261122190000) for accounts that still have saved insights and
// product analytics.
func startSavedInsights(ctx context.Context, store any, channels savedinsightnotify.ChannelSender, release *observeRelease, reg prometheus.Registerer, log *slog.Logger) {
	runStore, ok := store.(savedinsightnotify.Store)
	if !ok {
		log.Info("meterd: saved insights off", "store", ok)
		return
	}
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meterd_saved_insight_runs_total",
		Help: "Scheduled saved insight runs by outcome: ok, failed (the query failed; the alert keeps its state and a due digest is retried) or not_released (the account no longer has saved insights). ADR-20261122190000.",
	}, []string{"outcome"})
	for _, o := range []string{"ok", "failed", "not_released"} {
		c.WithLabelValues(o).Add(0)
	}
	if reg != nil {
		reg.MustRegister(c)
	}
	r := &savedinsightnotify.Runner{
		Store: runStore, Channels: channels, Log: log,
		Released: release.savedInsights, Outcome: func(outcome string) { c.WithLabelValues(outcome).Inc() },
	}
	go r.Run(ctx)
}
