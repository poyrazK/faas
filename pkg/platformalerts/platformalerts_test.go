package platformalerts_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformalerts"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeMetrics struct {
	rates, requests map[string]float64
	err             error
}

func (f fakeMetrics) QueryMap(_ context.Context, query string) (map[string]float64, error) {
	if f.err != nil {
		return nil, f.err
	}
	if strings.Contains(query, " / ") {
		return f.rates, nil
	}
	return f.requests, nil
}

type sentMail struct{ to, subject, body, key string }

type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
}

func (m *fakeMailer) SendEmail(_ context.Context, to, subject, body, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, sentMail{to, subject, body, key})
	return nil
}

func seedApp(t *testing.T, store *state.MemStore, slug string) (state.Account, state.App) {
	t.Helper()
	acct, err := store.CreateAccount(t.Context(), slug+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return acct, app
}

// An app answering only 5xx gets one email; a quiet app, a healthy app and
// an app with its own error-rate rule get none (ADR-20261115194000).
func TestWorker_OutageEmailsOnceAndSkipsCoveredApps(t *testing.T) {
	store := state.NewMemStore()
	acct, down := seedApp(t, store, "frontend")
	_, quiet := seedApp(t, store, "quiet")
	_, healthy := seedApp(t, store, "healthy")
	ownerAcct, owned := seedApp(t, store, "owned")
	if _, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: ownerAcct.ID, AppID: owned.ID, Name: "errors",
		Enabled: true, Metric: "error_rate_pct", Comparison: state.AlertComparison("gt"), Threshold: 5,
		WindowSpec: state.AlertWindow5m, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("x"), CooldownMinutes: 30}); err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}
	// A stage's error-rate rule watches the stage, not production
	// (ADR-20261104120000): frontend still gets the email.
	if _, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: acct.ID, AppID: down.ID, EnvironmentID: "env-staging", Name: "stage errors",
		Enabled: true, Metric: "error_rate_pct", Comparison: state.AlertComparison("gt"), Threshold: 5,
		WindowSpec: state.AlertWindow5m, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("x"), CooldownMinutes: 30}); err != nil {
		t.Fatalf("CreateAlertRule (stage): %v", err)
	}
	metrics := fakeMetrics{
		rates:    map[string]float64{down.ID: 100, quiet.ID: 100, healthy.ID: 2, owned.ID: 95, "unknown-app": 100},
		requests: map[string]float64{down.ID: 340, quiet.ID: 3, healthy.ID: 900, owned.ID: 500, "unknown-app": 50},
	}
	mail := &fakeMailer{}
	w := &platformalerts.Worker{Store: store, Metrics: metrics, Mail: mail}

	for range 2 {
		if err := w.DetectOutages(t.Context()); err != nil {
			t.Fatalf("DetectOutages: %v", err)
		}
		if err := w.SendDue(t.Context()); err != nil {
			t.Fatalf("SendDue: %v", err)
		}
	}
	if len(mail.sent) != 1 {
		t.Fatalf("sent %d emails, want 1: %+v", len(mail.sent), mail.sent)
	}
	got := mail.sent[0]
	if got.to != acct.Email || !strings.Contains(got.subject, "frontend is failing") ||
		!strings.Contains(got.body, "100.0% of its last 340 requests") || !strings.Contains(got.body, "gregale debug status frontend") {
		t.Fatalf("email = %+v", got)
	}
}

// An enabled burn alert on an availability SLO (the protection baseline's,
// ADR-20261116120000 §7) covers the app; a burn alert on a latency SLO does
// not.
func TestWorker_OutageSkipsAppsWithAvailabilityBurnAlert(t *testing.T) {
	store := state.NewMemStore()
	burn := func(acct state.Account, app state.App, sli string, threshold int) {
		t.Helper()
		slo, err := store.CreateSLO(t.Context(), state.SLO{AccountID: acct.ID, AppID: app.ID, Name: sli, SLI: sli,
			LatencyThresholdMS: threshold, ObjectiveBP: 9950, WindowDays: 30}, api.MaxSLOsPerApp)
		if err != nil {
			t.Fatalf("CreateSLO: %v", err)
		}
		if _, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: acct.ID, AppID: app.ID, Name: sli + "-burn",
			Enabled: true, Metric: state.AlertMetricSLOBudgetBurn, Comparison: state.AlertGt, Threshold: 14.4,
			WindowSpec: state.AlertWindowSpec("1h"), SLOID: slo.ID, WebhookURL: "https://example.com/hook",
			WebhookSecretSealed: []byte("x"), CooldownMinutes: 30}); err != nil {
			t.Fatalf("CreateAlertRule: %v", err)
		}
	}
	availAcct, protected := seedApp(t, store, "protected")
	burn(availAcct, protected, state.SLIAvailability, 0)
	latencyAcct, slowOnly := seedApp(t, store, "slowonly")
	burn(latencyAcct, slowOnly, state.SLILatency, 1000)
	metrics := fakeMetrics{
		rates:    map[string]float64{protected.ID: 100, slowOnly.ID: 100},
		requests: map[string]float64{protected.ID: 300, slowOnly.ID: 300},
	}
	mail := &fakeMailer{}
	w := &platformalerts.Worker{Store: store, Metrics: metrics, Mail: mail}
	if err := w.DetectOutages(t.Context()); err != nil {
		t.Fatalf("DetectOutages: %v", err)
	}
	if err := w.SendDue(t.Context()); err != nil {
		t.Fatalf("SendDue: %v", err)
	}
	if len(mail.sent) != 1 || mail.sent[0].to != latencyAcct.Email {
		t.Fatalf("sent = %+v; want one email, to the latency-only app's owner", mail.sent)
	}
}

func queueCronPaused(t *testing.T, store *state.MemStore) (state.Account, state.Cron) {
	t.Helper()
	acct, app := seedApp(t, store, "orders")
	c, err := store.CreateCron(t.Context(), app.ID, "*/15 * * * *", "/inventory", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	if _, err := store.QueuePlatformAlertNotice(t.Context(), state.PlatformAlertNotice{
		AccountID: acct.ID, AppID: app.ID, Kind: state.PlatformAlertCronFailingPaused, SubjectID: c.ID,
		Detail: []byte(`{"path":"/inventory","failed_runs":10,"since":"2026-11-14T10:00:00Z","last_error":"HTTP 404"}`),
	}, api.PlatformAlertCooldown); err != nil {
		t.Fatalf("QueuePlatformAlertNotice: %v", err)
	}
	return acct, c
}

// A cron schedd paused is emailed with how to resume it.
func TestWorker_CronPausedEmail(t *testing.T) {
	store := state.NewMemStore()
	acct, c := queueCronPaused(t, store)
	mail := &fakeMailer{}
	w := &platformalerts.Worker{Store: store, Mail: mail}
	if err := w.SendDue(t.Context()); err != nil {
		t.Fatalf("SendDue: %v", err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mail.sent))
	}
	got := mail.sent[0]
	if got.to != acct.Email || !strings.Contains(got.subject, "paused a failing cron on orders") ||
		!strings.Contains(got.body, "POST /inventory") || !strings.Contains(got.body, "Last error: HTTP 404") ||
		!strings.Contains(got.body, "gregale crons update "+c.ID+" --enable") {
		t.Fatalf("email = %+v", got)
	}
	if again, err := store.ClaimPlatformAlertNotices(t.Context(), 10, time.Minute); err != nil || len(again) != 0 {
		t.Fatalf("a sent notice is still due: %+v, %v", again, err)
	}
}

// A mail outage puts the notice back for later instead of dropping it.
func TestWorker_MailFailureRetriesLater(t *testing.T) {
	store := state.NewMemStore()
	queueCronPaused(t, store)
	var outcomes []string
	w := &platformalerts.Worker{Store: store, Mail: &fakeMailer{err: errors.New("smtp down")},
		Outcome: func(_, o string) { outcomes = append(outcomes, o) }}
	if err := w.SendDue(t.Context()); err != nil {
		t.Fatalf("SendDue: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0] != platformalerts.OutcomeRetry {
		t.Fatalf("outcomes = %v, want one retry", outcomes)
	}
	if again, err := store.ClaimPlatformAlertNotices(t.Context(), 10, time.Minute); err != nil || len(again) != 0 {
		t.Fatalf("a retried notice was due at once: %+v, %v", again, err)
	}
	if queued, err := store.QueuePlatformAlertNotice(t.Context(), state.PlatformAlertNotice{}, 0); err == nil && queued {
		t.Fatal("an empty notice was queued")
	}
}
