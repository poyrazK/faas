package alerts_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019193500 — a budget alert reaches every bound channel with exact
// money text; a failing channel is recorded and retried without resending
// to the channel that already accepted it.
func TestDeliverFinancialBudgetChannels(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "budget@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(ident.Recipient(), alertchannels.SealNamespace, []byte("https://hooks.slack.com/services/T1/B2/secret"), 512)
	if err != nil {
		t.Fatal(err)
	}
	slack, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "ops", Kind: "slack", TargetSealed: sealed}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	email, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "finance", Kind: "email", Email: "finance@example.com"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "monthly usage", Scope: financial.BudgetScope{Kind: "account"}, Currency: financial.BillingCurrency, Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1234567, NotifyMillicents: []int64{80000}, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: true}
	p, err := store.CreateFinancialBudget(ctx, acct.ID, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFinancialBudgetChannels(ctx, acct.ID, p.ID, []string{slack.ID, email.ID}); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	evaluated := time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC)
	known := int64(90123)
	e := financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: evaluated, EvaluatedAt: evaluated, EvidenceThroughID: 1, KnownMillicents: &known, Fresh: true, CoverageComplete: true}
	if err := store.RecordFinancialBudgetEvaluation(ctx, acct.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}

	channels := &recordingChannels{fail: map[string]error{"email": errors.New("mailbox full")}}
	var outcomes []string
	now := evaluated.Add(time.Hour)
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:            store,
		Identity:         func() *age.X25519Identity { return ident },
		Channels:         channels,
		ChannelOutcome:   func(kind, outcome string) { outcomes = append(outcomes, kind+"="+outcome) },
		DashboardBaseURL: "https://gregale.dev",
		Now:              func() time.Time { return now },
		Log:              discardLog(),
	})
	if err := ev.DeliverFinancialBudgetChannelsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(channels.sent) != 2 {
		t.Fatalf("sent %d notifications, want 2", len(channels.sent))
	}
	channelIDs := map[string]string{"slack": slack.ID, "email": email.ID}
	for _, s := range channels.sent {
		m := s.msg
		if m.Event != alertchannels.EventFire || m.DedupKey != "budget-"+p.ID+"-2026-10" || m.Severity != "warning" || m.DashboardURL != "https://gregale.dev/dashboard/billing" ||
			!strings.Contains(m.Summary, "USD 0.90123") || !strings.Contains(m.Summary, "USD 0.80 threshold") || !strings.Contains(m.Summary, "USD 12.34567 budget") ||
			m.Threshold != 0 || m.Observed != 0 || !strings.HasPrefix(m.IdempotencyKey, "financial-budget-channel-") || !strings.HasSuffix(m.IdempotencyKey, "-"+channelIDs[s.target.Kind]) {
			t.Fatalf("message = %+v", m)
		}
		if s.target.Kind == "slack" && s.target.SlackURL != "https://hooks.slack.com/services/T1/B2/secret" {
			t.Fatalf("slack destination not unsealed: %+v", s.target)
		}
	}
	if strings.Join(outcomes, ",") != "slack=delivered,email=failed" && strings.Join(outcomes, ",") != "email=failed,slack=delivered" {
		t.Fatalf("outcomes = %v", outcomes)
	}
	chans, _ := store.ListFinancialBudgetChannels(ctx, acct.ID, p.ID)
	for _, c := range chans {
		if c.Kind == "email" && c.LastError != "mailbox full" || c.Kind == "slack" && c.LastDeliveredAt.IsZero() {
			t.Fatalf("channel status not recorded: %+v", c)
		}
	}

	// The failed email retries after backoff; Slack is not sent again.
	channels.sent, channels.fail = nil, nil
	if err := ev.DeliverFinancialBudgetChannelsOnce(ctx); err != nil || len(channels.sent) != 0 {
		t.Fatalf("retry ignored backoff: %d sent, %v", len(channels.sent), err)
	}
	now = now.Add(api.FinancialBudgetAlertRetryMax)
	if err := ev.DeliverFinancialBudgetChannelsOnce(ctx); err != nil || len(channels.sent) != 1 || channels.sent[0].target.Kind != "email" {
		t.Fatalf("retry: %+v, %v", channels.sent, err)
	}

	var none *alerts.Evaluator
	if err := none.DeliverFinancialBudgetChannelsOnce(ctx); err != nil {
		t.Fatalf("nil evaluator: %v", err)
	}
}

func TestFinancialBudgetChannelMessageAtLimit(t *testing.T) {
	known := int64(100000)
	a := state.FinancialBudgetAlertDelivery{PolicyID: "p", Spec: financial.BudgetSpec{Name: "previews", Scope: financial.BudgetScope{Kind: "app", ID: "app-1"}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 100000, Mode: "monitored", Action: "stop_previews", DrainSeconds: 30}}
	a.ID, a.ThresholdMillicents = "alert", 100000
	a.Evaluation = financial.BudgetEvaluation{PeriodStart: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), KnownMillicents: &known}
	m := alerts.FinancialBudgetChannelMessage(a, "chan", "")
	if m.Severity != "error" || m.DashboardURL != "" || !strings.Contains(m.Summary, "USD 1.00") || !strings.Contains(m.Summary, "previews stop") || !strings.Contains(m.Summary, "coverage is partial") || !strings.Contains(m.Summary, "app app-1") {
		t.Fatalf("message = %+v", m)
	}
	if alertchannels.Headline(m) != `Alert: budget "previews"` || alertchannels.Detail(m) != m.Summary {
		t.Fatalf("headline %q detail %q", alertchannels.Headline(m), alertchannels.Detail(m))
	}
}
