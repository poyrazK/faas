package financialtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// budgetChannelTestStore lists the store methods the channel suite calls
// rather than embedding state.Store (scripts/ci/teststorecheck).
type budgetChannelTestStore interface {
	accountCreator
	state.FinancialBudgetMonitorStore
	state.FinancialBudgetChannelStore
	state.NotificationChannelStore
	CreateFinancialBudget(context.Context, string, string, string, financial.BudgetSpec) (state.FinancialBudget, error)
	UpdateFinancialBudget(context.Context, string, string, int64, string, financial.BudgetSpec) (state.FinancialBudget, error)
}

// adr: 20261019193500 — budget alerts reach bound channels once each, with
// fenced retries, bounded attempts and cascading removal, in memory and PG.
func TestFinancialBudgetAlertChannels(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store budgetChannelTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			budgetChannelSuite(t, store)
		})
	}
}

func budgetChannelSuite(t *testing.T, store budgetChannelTestStore) {
	ctx := t.Context()
	a, other := financialAccount(t, store), financialAccount(t, store)
	channel := func(account, name string) state.NotificationChannel {
		t.Helper()
		c, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: account, Name: name, Kind: "email", Email: name + "@example.com", TargetHint: name + "@example.com"}, api.MaxNotificationChannelsPerAccount)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	ops, finance, foreign := channel(a.ID, "ops"), channel(a.ID, "finance"), channel(other.ID, "theirs")
	spec := financialBudgetSpec()
	spec.LimitMillicents, spec.NotifyMillicents = 100, []int64{80}
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFinancialBudgetChannels(ctx, a.ID, p.ID, []string{ops.ID, finance.ID, foreign.ID}); err != nil {
		t.Fatal(err)
	}
	bound, err := store.ListFinancialBudgetChannels(ctx, a.ID, p.ID)
	if err != nil || len(bound) != 2 || bound[0].ID != finance.ID || bound[1].ID != ops.ID {
		t.Fatalf("bound channels (foreign must be ignored): %+v, %v", bound, err)
	}
	if err := store.SetFinancialBudgetChannels(ctx, other.ID, p.ID, []string{foreign.ID}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign policy bound: %v", err)
	}
	if err := store.SetFinancialBudgetChannels(ctx, a.ID, p.ID, []string{"not-a-uuid"}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid channel id: %v", err)
	}

	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := month.Add(time.Hour)
	record := func(known int64) {
		t.Helper()
		now = now.Add(time.Minute)
		e := financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, EvidenceThroughID: now.Unix(), KnownMillicents: &known, Fresh: true, CoverageComplete: true}
		if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
			t.Fatal(err)
		}
	}
	claim := func() (state.FinancialBudgetAlertChannelDelivery, error) {
		return store.ClaimFinancialBudgetAlertChannel(ctx, now)
	}
	record(90)
	record(90) // a repeated crossing enqueues nothing new
	first, err := claim()
	if err != nil || first.Attempts != 1 || first.Alert.ThresholdMillicents != 80 || first.Alert.Spec.Name != spec.Name || first.Channel.Email == "" {
		t.Fatalf("first claim: %+v, %v", first, err)
	}
	second, err := claim()
	if err != nil || second.Channel.ID == first.Channel.ID || second.Alert.ID != first.Alert.ID {
		t.Fatalf("second claim: %+v, %v", second, err)
	}
	if _, err := claim(); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a delivery was enqueued twice: %v", err)
	}
	if err := store.SettleFinancialBudgetAlertChannel(ctx, first.Alert.ID, first.Channel.ID, uuid.NewString(), now, state.FinancialBudgetChannelDelivered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unfenced settle: %v", err)
	}
	if err := store.SettleFinancialBudgetAlertChannel(ctx, first.Alert.ID, first.Channel.ID, first.LeaseToken, now, "bounced"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unknown outcome: %v", err)
	}
	if err := store.SettleFinancialBudgetAlertChannel(ctx, first.Alert.ID, first.Channel.ID, first.LeaseToken, now, state.FinancialBudgetChannelDelivered); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleFinancialBudgetAlertChannel(ctx, second.Alert.ID, second.Channel.ID, second.LeaseToken, now, state.FinancialBudgetChannelFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := claim(); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("retry ignored backoff: %v", err)
	}

	// A failing channel is retried until FinancialBudgetChannelAttemptsMax
	// claims, then abandoned; the delivered one is never sent again.
	for attempt := 2; attempt <= api.FinancialBudgetChannelAttemptsMax; attempt++ {
		now = now.Add(api.FinancialBudgetAlertRetryMax + time.Minute)
		retry, err := claim()
		if err != nil || retry.Channel.ID != second.Channel.ID || retry.Attempts != attempt || retry.LastError != state.FinancialBudgetChannelFailed {
			t.Fatalf("retry %d: %+v, %v", attempt, retry, err)
		}
		if err := store.SettleFinancialBudgetAlertChannel(ctx, retry.Alert.ID, retry.Channel.ID, retry.LeaseToken, now, state.FinancialBudgetChannelFailed); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(api.FinancialBudgetAlertRetryMax + time.Minute)
	if d, err := claim(); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("abandoned delivery claimed: %+v, %v", d, err)
	}

	// A channel bound later still hears about a threshold crossed this month,
	// once. Unbinding holds its pending delivery, rebinding resumes it, and
	// deleting the channel removes it.
	late := channel(a.ID, "late")
	bind := func(ids ...string) {
		t.Helper()
		if err := store.SetFinancialBudgetChannels(ctx, a.ID, p.ID, ids); err != nil {
			t.Fatal(err)
		}
	}
	bind(ops.ID, late.ID)
	record(95)
	bind(ops.ID)
	if d, err := claim(); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unbound channel claimed: %+v, %v", d, err)
	}
	bind(ops.ID, late.ID)
	resumed, err := claim()
	if err != nil || resumed.Channel.ID != late.ID || resumed.Attempts != 1 {
		t.Fatalf("rebound channel: %+v, %v", resumed, err)
	}
	if err := store.DeleteNotificationChannel(ctx, a.ID, late.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(api.FinancialBudgetAlertLease)
	if d, err := claim(); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted channel claimed: %+v, %v", d, err)
	}
	if err := store.SetFinancialBudgetChannels(ctx, a.ID, p.ID, []string{finance.ID}); err != nil {
		t.Fatal(err)
	}

	// The limit threshold reaches the bound channel; a disabled policy holds it.
	record(120)
	limit, err := claim()
	if err != nil || limit.Channel.ID != finance.ID || limit.Alert.ThresholdMillicents != 100 {
		t.Fatalf("limit claim: %+v, %v", limit, err)
	}
	if err := store.SettleFinancialBudgetAlertChannel(ctx, limit.Alert.ID, limit.Channel.ID, limit.LeaseToken, now, state.FinancialBudgetChannelFailed); err != nil {
		t.Fatal(err)
	}
	spec.Enabled = false
	if _, err := store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "editor", spec); err != nil {
		t.Fatal(err)
	}
	now = now.Add(api.FinancialBudgetAlertRetryMax + time.Minute)
	if d, err := claim(); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disabled policy delivered: %+v, %v", d, err)
	}
	if err := store.SetFinancialBudgetChannels(ctx, a.ID, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if bound, err := store.ListFinancialBudgetChannels(ctx, a.ID, p.ID); err != nil || len(bound) != 0 {
		t.Fatalf("cleared channels: %+v, %v", bound, err)
	}
}
