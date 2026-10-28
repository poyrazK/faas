package routeinsights_test

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeinsights"
	"github.com/onebox-faas/faas/pkg/state"
)

// An app whose route insights capability is off for its account is claimed,
// so the rotation moves on, but gets no insights.
func TestEvaluatorSkipsUnreleasedApps(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	acct, appID := uuid.NewString(), uuid.NewString()
	store := &fakeStore{
		now:      now,
		accounts: map[string]state.Account{acct: {ID: acct, Plan: api.PlanPro}},
		apps:     map[string]state.App{appID: {ID: appID, AccountID: acct, Slug: "shop", Type: state.AppTypeApp, RAMMB: 256}},
		busyApp:  appID,
	}
	released := 0
	e := &routeinsights.Evaluator{Store: store, Now: func() time.Time { return now },
		Released: func(state.Account, state.App) bool { released++; return false }}
	stats, err := e.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Apps != 1 || stats.Skipped != 1 || stats.Observed != 0 || len(store.upserts) != 0 || released != 1 {
		t.Fatalf("stats = %+v, upserts = %d, release checks = %d; want one claimed, skipped app and no insights", stats, len(store.upserts), released)
	}
	if _, ok := store.evaluated[appID]; !ok {
		t.Fatal("the unreleased app was not claimed, so the rotation would stall on it")
	}
}
