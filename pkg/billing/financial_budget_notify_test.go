package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type recordingBudgetMailer struct {
	sent []mail.Message
	err  error
}

func (m *recordingBudgetMailer) Send(_ context.Context, msg mail.Message) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

type budgetNotifyFixture struct {
	store   *visibilityTestStore
	account state.Account
	start   time.Time
}

// newBudgetNotifyFixture records 150 millicents of net compute (100 + 100 mb-s
// at 1 millicent each, less a 50 mb-s allowance) in October 2026.
func newBudgetNotifyFixture(t *testing.T) budgetNotifyFixture {
	t.Helper()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store := &visibilityTestStore{MemStore: state.NewMemStore(), retained: start, timeNow: start.Add(24 * time.Hour)}
	account, err := store.CreateAccount(t.Context(), "budget-notify@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "api", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MaxConcurrency: 1, WorkloadClass: state.WorkloadClassHTTP})
	if err != nil {
		t.Fatal(err)
	}
	price := state.FinancialPriceSnapshot{AccountID: account.ID, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), Plan: api.PlanPro, EffectiveFrom: start, DeliveryMode: "live", Price: financial.Price{Version: "pro-v1", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1, IncludedQuantity: 50}}
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), price); err != nil {
		t.Fatal(err)
	}
	for _, minute := range []time.Time{start, start.Add(time.Minute)} {
		if err := store.AppendUsage(t.Context(), account.ID, app.ID, uuid.NewString(), minute, 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	for at := start; at.Before(store.timeNow); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(t.Context(), at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	return budgetNotifyFixture{store: store, account: account, start: start}
}

func (f budgetNotifyFixture) budget(t *testing.T, limit int64, notify []int64, action string, enabled bool) state.FinancialBudget {
	t.Helper()
	spec := financial.BudgetSpec{Name: "Monthly guard", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: limit, NotifyMillicents: notify, Mode: "monitored", Action: action, ResumeRule: "manual", Enabled: enabled}
	if action != "notify" {
		spec.DrainSeconds = 30
	}
	p, err := f.store.CreateFinancialBudget(t.Context(), f.account.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f budgetNotifyFixture) notifications(t *testing.T) []state.FinancialBudgetNotification {
	t.Helper()
	rows, err := f.store.ListPendingFinancialBudgetNotifications(t.Context(), api.FinancialBudgetNotifyBatch)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// adr: 1500 — crossed thresholds notify once per budget and month, in one
// email naming the highest threshold; uncrossed thresholds and stopping or
// disabled budgets send nothing.
func TestBudgetNotifierSendsOnceForCrossedThresholds(t *testing.T) {
	f := newBudgetNotifyFixture(t)
	crossed := f.budget(t, 140, []int64{50, 100}, "notify", true)
	f.budget(t, 1_000, []int64{200}, "notify", true)             // nothing crossed
	f.budget(t, 100, []int64{50}, "notify", false)               // draft
	f.budget(t, 100, []int64{50}, "suspend_workloads", false)    // stopping draft
	mailer := &recordingBudgetMailer{}
	reg := prometheus.NewRegistry()
	n := NewBudgetNotifier(f.store, mailer, nil, func() time.Time { return f.store.timeNow }, reg)
	for range 3 {
		if err := n.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1: %+v", len(mailer.sent), mailer.sent)
	}
	msg := mailer.sent[0]
	if len(msg.To) != 1 || msg.To[0] != f.account.Email || !strings.Contains(msg.Subject, `"Monthly guard" passed $0.0014`) {
		t.Fatalf("email = %+v, want the account address and the limit as the highest threshold", msg)
	}
	if !strings.Contains(msg.TextBody, "Known spending so far: $0.0015") || strings.Contains(msg.TextBody, "may be higher") {
		t.Fatalf("body = %s", msg.TextBody)
	}
	if want := "budget:" + crossed.ID + ":2026-10:140"; msg.MessageID != want {
		t.Fatalf("message id = %q, want %q", msg.MessageID, want)
	}
	if pending := f.notifications(t); len(pending) != 0 {
		t.Fatalf("pending after send: %+v", pending)
	}
	if got := testutil.ToFloat64(n.outcome.WithLabelValues(state.FinancialBudgetNotificationSent)); got != 1 {
		t.Fatalf("sent outcomes = %v, want 1", got)
	}
}

// adr: 1500 — a failed send stays pending and is retried; after the attempt
// budget it is abandoned and never sent.
func TestBudgetNotifierRetriesThenAbandons(t *testing.T) {
	f := newBudgetNotifyFixture(t)
	f.budget(t, 100, nil, "notify", true)
	mailer := &recordingBudgetMailer{err: errors.New("provider unavailable")}
	n := NewBudgetNotifier(f.store, mailer, nil, func() time.Time { return f.store.timeNow }, nil)
	if err := n.RunOnce(t.Context()); err == nil {
		t.Fatal("failed send reported success")
	}
	pending := f.notifications(t)
	if len(pending) != 1 || pending[0].Attempts != 1 || pending[0].LastError != "provider unavailable" {
		t.Fatalf("after one failure: %+v", pending)
	}
	for range api.FinancialBudgetNotifyMaxAttempts {
		_ = n.RunOnce(t.Context())
	}
	if pending := f.notifications(t); len(pending) != 0 {
		t.Fatalf("still pending after %d attempts: %+v", api.FinancialBudgetNotifyMaxAttempts, pending)
	}
	mailer.err = nil
	if err := n.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("abandoned notification was sent later: %+v", mailer.sent)
	}
	if got := testutil.ToFloat64(n.outcome.WithLabelValues(state.FinancialBudgetNotificationAbandoned)); got != 1 {
		t.Fatalf("abandoned outcomes = %v, want 1", got)
	}
}

// adr: 1500 — a budget disabled between claim and delivery cancels its
// pending notification instead of sending it.
func TestBudgetNotifierCancelsDisabledBudget(t *testing.T) {
	f := newBudgetNotifyFixture(t)
	p := f.budget(t, 100, nil, "notify", true)
	mailer := &recordingBudgetMailer{err: errors.New("provider unavailable")}
	n := NewBudgetNotifier(f.store, mailer, nil, func() time.Time { return f.store.timeNow }, nil)
	_ = n.RunOnce(t.Context())
	disabled := p.Spec
	disabled.Enabled = false
	if _, err := f.store.UpdateFinancialBudget(t.Context(), f.account.ID, p.ID, p.Revision, "test", disabled); err != nil {
		t.Fatal(err)
	}
	mailer.err = nil
	if err := n.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 0 || len(f.notifications(t)) != 0 {
		t.Fatalf("disabled budget: sent %+v, pending %+v", mailer.sent, f.notifications(t))
	}
}

// adr: 1500 — a new month starts with no claims, and a threshold added later
// in the month notifies on its own.
func TestBudgetNotifierNewThresholdAndNewMonth(t *testing.T) {
	f := newBudgetNotifyFixture(t)
	p := f.budget(t, 1_000, []int64{100}, "notify", true)
	mailer := &recordingBudgetMailer{}
	now := f.store.timeNow
	n := NewBudgetNotifier(f.store, mailer, nil, func() time.Time { return now }, nil)
	if err := n.RunOnce(t.Context()); err != nil || len(mailer.sent) != 1 {
		t.Fatalf("first threshold: %v, sent %d", err, len(mailer.sent))
	}
	edited := p.Spec
	edited.NotifyMillicents = []int64{100, 120}
	if _, err := f.store.UpdateFinancialBudget(t.Context(), f.account.ID, p.ID, p.Revision, "test", edited); err != nil {
		t.Fatal(err)
	}
	if err := n.RunOnce(t.Context()); err != nil || len(mailer.sent) != 2 || !strings.Contains(mailer.sent[1].Subject, "$0.0012") {
		t.Fatalf("added threshold: %v, sent %+v", err, mailer.sent)
	}
	now = f.start.AddDate(0, 1, 1)
	if err := n.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 2 {
		t.Fatalf("November has no usage yet but sent %+v", mailer.sent[2:])
	}
}

func TestFinancialBudgetThresholds(t *testing.T) {
	got := financialBudgetThresholds(financial.BudgetSpec{NotifyMillicents: []int64{50, 100}, LimitMillicents: 100})
	if len(got) != 2 || got[0] != 50 || got[1] != 100 {
		t.Fatalf("thresholds = %v, want [50 100]", got)
	}
}
