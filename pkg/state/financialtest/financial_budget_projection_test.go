package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type budgetProjectionTestStore interface {
	budgetChannelTestStore
	state.FinancialBudgetProjectionStore
}

// adr: 20261019193500 — a projected overrun is raised once per revision and
// month, only from complete fresh evidence below the limit, reaches the
// account email and bound channels, and never displaces the limit alert.
func TestFinancialBudgetProjectionAlerts(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store budgetProjectionTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			budgetProjectionSuite(t, store)
		})
	}
}

func budgetProjectionSuite(t *testing.T, store budgetProjectionTestStore) {
	ctx := t.Context()
	a := financialAccount(t, store)
	ops, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: a.ID, Name: "ops", Kind: "email", Email: "ops@example.com", TargetHint: "ops@example.com"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	spec := financialBudgetSpec()
	spec.LimitMillicents, spec.NotifyMillicents = 100, []int64{80}
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFinancialBudgetChannels(ctx, a.ID, p.ID, []string{ops.ID}); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := month.Add(48 * time.Hour)
	record := func(known, projected int64, fresh bool) {
		t.Helper()
		now = now.Add(time.Minute)
		e := financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, EvidenceThroughID: now.Unix(), KnownMillicents: &known, ProjectedMillicents: &projected, Fresh: fresh, CoverageComplete: true}
		if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
			t.Fatal(err)
		}
	}
	runtime := func() financial.BudgetRuntime {
		t.Helper()
		r, err := store.FinancialBudgetRuntime(ctx, a.ID, p.ID, p.Revision, month)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	record(50, 90, true)   // projected below the limit
	record(50, 150, false) // stale evidence never projects
	if r := runtime(); r.Projection != nil {
		t.Fatalf("projection without an overrun: %+v", r.Projection)
	}
	record(50, 150, true)
	record(55, 160, true) // the month keeps one projection
	r := runtime()
	if r.Projection == nil || r.Projection.Kind != financial.BudgetAlertProjection || r.Projection.ThresholdMillicents != 100 || *r.Projection.Evaluation.ProjectedMillicents != 150 || len(r.Alerts) != 0 {
		t.Fatalf("projection: %+v alerts %+v", r.Projection, r.Alerts)
	}

	email, err := store.ClaimFinancialBudgetProjection(ctx, now)
	if err != nil || email.ID != r.Projection.ID || email.Kind != financial.BudgetAlertProjection || email.Spec.Name != spec.Name {
		t.Fatalf("projection email claim: %+v, %v", email, err)
	}
	if _, err := store.ClaimFinancialBudgetProjection(ctx, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("projection claimed twice: %v", err)
	}
	if err := store.SettleFinancialBudgetProjection(ctx, email.ID, uuid.NewString(), now, true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unfenced settle: %v", err)
	}
	if err := store.SettleFinancialBudgetProjection(ctx, email.ID, email.LeaseToken, now, true); err != nil {
		t.Fatal(err)
	}
	if r := runtime(); r.Projection.State != "delivered" {
		t.Fatalf("delivered projection: %+v", r.Projection)
	}
	channel, err := store.ClaimFinancialBudgetAlertChannel(ctx, now)
	if err != nil || channel.Alert.Kind != financial.BudgetAlertProjection || channel.Alert.ID != email.ID || channel.Channel.ID != ops.ID || channel.Alert.Evaluation.ProjectedMillicents == nil {
		t.Fatalf("projection channel claim: %+v, %v", channel, err)
	}
	if err := store.SettleFinancialBudgetAlertChannel(ctx, channel.Alert.ID, channel.Channel.ID, channel.LeaseToken, now, state.FinancialBudgetChannelDelivered); err != nil {
		t.Fatal(err)
	}

	// Reaching the limit records the 80 and 100 threshold alerts beside the
	// projection; the projection does not displace the limit alert.
	record(120, 200, true)
	r = runtime()
	if len(r.Alerts) != 2 || r.Alerts[1].ThresholdMillicents != 100 || r.Alerts[1].Kind != "" || r.Projection == nil || r.Projection.ID != email.ID {
		t.Fatalf("limit alert beside projection: %+v %+v", r.Alerts, r.Projection)
	}
	limit, err := store.ClaimFinancialBudgetAlert(ctx, now)
	if err != nil || limit.Kind != "" {
		t.Fatalf("threshold email claim: %+v, %v", limit, err)
	}
	if _, err := store.ClaimFinancialBudgetProjection(ctx, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delivered projection resent: %v", err)
	}
}
