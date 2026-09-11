package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppLogDrainHealthRoundTripAndCascade(t *testing.T) {
	s, ctx := pgStore(t)
	suffix := uuid.NewString()
	account, err := s.CreateAccount(ctx, "pg-log-drain-health-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-log-drain-health-" + suffix})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	drain, err := s.CreateAppLogDrain(ctx, state.AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/health-" + suffix, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	lastSuccess := time.Date(2026, 9, 11, 16, 0, 0, 123456000, time.UTC)
	lastFailure := lastSuccess.Add(time.Minute)
	if err := s.UpsertAppLogDrainHealth(ctx, state.AppLogDrainHealth{
		DrainID: drain.ID, Status: "degraded", Active: true, QueueDepth: 4,
		QueueCapacity: 32, DeliveredTotal: 10, FailedTotal: 2,
		DroppedTotal: 1, RetriesTotal: 3, StreamReconnectsTotal: 2,
		GapsTotal: 1, LastSuccessAt: lastSuccess, LastFailureAt: lastFailure,
		LastError: "source log gap observed", UpdatedAt: lastFailure,
	}); err != nil {
		t.Fatalf("UpsertAppLogDrainHealth: %v", err)
	}

	got, err := s.AppLogDrainHealthByDrainID(ctx, drain.ID)
	if err != nil {
		t.Fatalf("AppLogDrainHealthByDrainID: %v", err)
	}
	if got.Status != "degraded" || !got.Active || got.QueueDepth != 4 || got.QueueCapacity != 32 || got.DeliveredTotal != 10 || got.FailedTotal != 2 || got.DroppedTotal != 1 || got.RetriesTotal != 3 || got.StreamReconnectsTotal != 2 || got.GapsTotal != 1 {
		t.Fatalf("health counters = %+v", got)
	}
	if !got.LastSuccessAt.Equal(lastSuccess) || !got.LastFailureAt.Equal(lastFailure) || got.LastError != "source log gap observed" {
		t.Fatalf("health event fields = %+v", got)
	}
	rows, err := s.ListAppLogDrainHealthForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListAppLogDrainHealthForApp: %v", err)
	}
	if len(rows) != 1 || rows[0].DrainID != drain.ID {
		t.Fatalf("health rows = %+v, want one row for %s", rows, drain.ID)
	}

	if err := s.DeleteAppLogDrain(ctx, drain.ID); err != nil {
		t.Fatalf("DeleteAppLogDrain: %v", err)
	}
	if _, err := s.AppLogDrainHealthByDrainID(ctx, drain.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("health after drain delete = %v, want ErrNotFound", err)
	}
}
