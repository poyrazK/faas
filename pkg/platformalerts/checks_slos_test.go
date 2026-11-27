package platformalerts_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/platformalerts"
	"github.com/onebox-faas/faas/pkg/state"
)

func addCheck(t *testing.T, store *state.MemStore, acct state.Account, app state.App, name string, runs ...bool) state.SyntheticCheck {
	t.Helper()
	c, err := store.CreateSyntheticCheck(t.Context(), state.SyntheticCheck{AccountID: acct.ID, AppID: app.ID, Name: name,
		Method: "GET", Path: "/cart", ExpectedStatus: 200, TimeoutMS: 5000, IntervalSeconds: 300, Enabled: true}, 10)
	if err != nil {
		t.Fatalf("CreateSyntheticCheck: %v", err)
	}
	// Runs end about five minutes ago: fresh against the 300 s interval.
	start := time.Now().Add(-time.Duration(len(runs)) * 5 * time.Minute)
	for i, ok := range runs {
		run := state.SyntheticCheckRun{CheckID: c.ID, StartedAt: start.Add(time.Duration(i) * 5 * time.Minute), OK: ok, StatusCode: 200}
		if !ok {
			run.StatusCode, run.ErrorClass = 500, "status"
		}
		if err := store.RecordSyntheticCheckRun(t.Context(), run); err != nil {
			t.Fatalf("RecordSyntheticCheckRun: %v", err)
		}
	}
	return c
}

// A check failing its last runs emails the owner once; a recovered check, a
// check with too few runs and a check with its own alert rule do not.
func TestWorker_FailingCheckEmailsOnceAndSkipsCovered(t *testing.T) {
	store := state.NewMemStore()
	acct, app := seedApp(t, store, "frontend")
	failing := addCheck(t, store, acct, app, "cart", true, false, false, false)
	addCheck(t, store, acct, app, "recovered", false, false, false, true)
	addCheck(t, store, acct, app, "young", false, false)
	covered := addCheck(t, store, acct, app, "covered", false, false, false)
	if _, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: acct.ID, AppID: app.ID, Name: "checkout",
		Enabled: true, Metric: state.AlertMetric("synthetic_check_consecutive_failures"), SyntheticCheckID: covered.ID,
		Comparison: state.AlertComparison("gt"), Threshold: 2, WindowSpec: state.AlertWindow5m,
		WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("x"), CooldownMinutes: 30}); err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}
	mail := &fakeMailer{}
	w := &platformalerts.Worker{Store: store, Mail: mail}
	for range 2 {
		if err := w.DetectFailingChecks(t.Context()); err != nil {
			t.Fatalf("DetectFailingChecks: %v", err)
		}
		if err := w.SendDue(t.Context()); err != nil {
			t.Fatalf("SendDue: %v", err)
		}
	}
	if len(mail.sent) != 1 {
		t.Fatalf("sent %d emails, want 1: %+v", len(mail.sent), mail.sent)
	}
	got := mail.sent[0]
	if got.to != acct.Email || !strings.Contains(got.subject, "check cart on frontend is failing") ||
		!strings.Contains(got.body, "GET /cart") || !strings.Contains(got.body, "failed its last 3 runs") ||
		!strings.Contains(got.body, "--synthetic-check "+failing.ID) {
		t.Fatalf("email = %+v", got)
	}
}

// fakeSLOMetrics answers the SLO availability queries per app: good (2xx)
// and total (2xx|5xx) counts.
type fakeSLOMetrics struct {
	fakeMetrics
	good, total map[string]float64
}

func (f fakeSLOMetrics) QueryScalar(_ context.Context, query string) (float64, error) {
	pick := f.total
	if strings.Contains(query, `class="2xx"`) {
		pick = f.good
	}
	for app, v := range pick {
		if strings.Contains(query, app) {
			return v, nil
		}
	}
	return 0, nil
}

// An SLO burning fast with no rule naming it emails the owner; a healthy
// SLO and one with its own rule do not.
func TestWorker_BurningSLOEmailsAndSkipsCovered(t *testing.T) {
	store := state.NewMemStore()
	acct, burning := seedApp(t, store, "frontend")
	_, healthy := seedApp(t, store, "catalog")
	ownAcct, owned := seedApp(t, store, "checkout")
	mk := func(a state.Account, app state.App) state.SLO {
		s, err := store.CreateSLO(t.Context(), state.SLO{AccountID: a.ID, AppID: app.ID, Name: "availability",
			SLI: state.SLIAvailability, ObjectiveBP: 9990, WindowDays: 30}, 10)
		if err != nil {
			t.Fatalf("CreateSLO: %v", err)
		}
		return s
	}
	burningSLO := mk(acct, burning)
	mk(acct, healthy)
	ownedSLO := mk(ownAcct, owned)
	if _, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: ownAcct.ID, AppID: owned.ID, Name: "burn",
		Enabled: true, Metric: state.AlertMetricSLOBudgetBurn, SLOID: ownedSLO.ID,
		Comparison: state.AlertComparison("gt"), Threshold: 6, WindowSpec: state.AlertWindow5m,
		WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("x"), CooldownMinutes: 30}); err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}
	metrics := fakeSLOMetrics{
		good:  map[string]float64{burning.ID: 100, healthy.ID: 1000, owned.ID: 0},
		total: map[string]float64{burning.ID: 400, healthy.ID: 1000, owned.ID: 300},
	}
	mail := &fakeMailer{}
	w := &platformalerts.Worker{Store: store, Metrics: metrics, Mail: mail}
	if err := w.DetectBurningSLOs(t.Context()); err != nil {
		t.Fatalf("DetectBurningSLOs: %v", err)
	}
	if err := w.SendDue(t.Context()); err != nil {
		t.Fatalf("SendDue: %v", err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("sent %d emails, want 1: %+v", len(mail.sent), mail.sent)
	}
	got := mail.sent[0]
	if !strings.Contains(got.subject, "SLO availability on frontend is burning") ||
		!strings.Contains(got.body, "--slo "+burningSLO.ID) || !strings.Contains(got.body, "objective 99.90%") {
		t.Fatalf("email = %+v", got)
	}
}

func TestWorker_FailingCheckSkipsUnreleasedAndStale(t *testing.T) {
	store := state.NewMemStore()
	acct, app := seedApp(t, store, "frontend")
	unreleased := addCheck(t, store, acct, app, "unreleased", false, false, false)
	stale, err := store.CreateSyntheticCheck(t.Context(), state.SyntheticCheck{AccountID: acct.ID, AppID: app.ID, Name: "stale",
		Method: "GET", Path: "/old", ExpectedStatus: 200, TimeoutMS: 5000, IntervalSeconds: 300, Enabled: true}, 10)
	if err != nil {
		t.Fatalf("CreateSyntheticCheck: %v", err)
	}
	old := time.Now().Add(-3 * time.Hour)
	for i := range 3 {
		if err := store.RecordSyntheticCheckRun(t.Context(), state.SyntheticCheckRun{CheckID: stale.ID,
			StartedAt: old.Add(time.Duration(i) * 5 * time.Minute), StatusCode: 500, ErrorClass: "status"}); err != nil {
			t.Fatalf("RecordSyntheticCheckRun: %v", err)
		}
	}
	mail := &fakeMailer{}
	w := &platformalerts.Worker{Store: store, Mail: mail,
		CheckReleased: func(_ context.Context, c state.RunnableSyntheticCheck) bool { return c.ID != unreleased.ID }}
	if err := w.DetectFailingChecks(t.Context()); err != nil {
		t.Fatalf("DetectFailingChecks: %v", err)
	}
	if err := w.SendDue(t.Context()); err != nil {
		t.Fatalf("SendDue: %v", err)
	}
	if len(mail.sent) != 0 {
		t.Fatalf("sent %d emails for an unreleased and a stale check, want 0: %+v", len(mail.sent), mail.sent)
	}
}

func TestWorker_BurningSLOSkipsUnreleased(t *testing.T) {
	store := state.NewMemStore()
	acct, app := seedApp(t, store, "frontend")
	if _, err := store.CreateSLO(t.Context(), state.SLO{AccountID: acct.ID, AppID: app.ID, Name: "availability",
		SLI: state.SLIAvailability, ObjectiveBP: 9990, WindowDays: 30}, 10); err != nil {
		t.Fatalf("CreateSLO: %v", err)
	}
	metrics := fakeSLOMetrics{good: map[string]float64{app.ID: 100}, total: map[string]float64{app.ID: 400}}
	mail := &fakeMailer{}
	w := &platformalerts.Worker{Store: store, Metrics: metrics, Mail: mail,
		SLOReleased: func(context.Context, state.SLO) bool { return false }}
	if err := w.DetectBurningSLOs(t.Context()); err != nil {
		t.Fatalf("DetectBurningSLOs: %v", err)
	}
	if err := w.SendDue(t.Context()); err != nil {
		t.Fatalf("SendDue: %v", err)
	}
	if len(mail.sent) != 0 {
		t.Fatalf("sent %d emails for an SLO the plan no longer covers, want 0", len(mail.sent))
	}
}
