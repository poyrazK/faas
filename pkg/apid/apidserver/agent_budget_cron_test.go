// adr: 20261111114059 — cost-control review 2026-11-19 (N3): a cron that
// fires more often than its app idles keeps the app running, so an agent's
// budget prices it.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCronResidentSecondsPerMonth(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 30, 0, time.UTC)
	month := int64(api.AgentBudgetHoursPerMonth) * 3600
	for _, tc := range []struct {
		name, schedule string
		hold           time.Duration
		want           int64
	}{
		{"every minute outlasts a 60 s idle", "* * * * *", 60 * time.Second, month},
		{"every minute with a longer idle is still the whole month", "* * * * *", 300 * time.Second, month},
		{"hourly holds a minute an hour", "0 * * * *", 60 * time.Second, month / 60},
		{"every five minutes with a 60 s idle", "*/5 * * * *", 60 * time.Second, month / 5},
		{"no hold prices nothing", "* * * * *", 0, 0},
		{"an invalid schedule prices nothing", "not a cron", time.Minute, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := apidserver.CronResidentSecondsPerMonth(tc.schedule, "UTC", tc.hold, now); got != tc.want {
				t.Fatalf("resident seconds = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHandlers_AgentBudgetPricesCrons(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "cron-budget@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cron-target", Type: state.AppTypeApp, RAMMB: 2048, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "cron-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	// Only the budget asks: the agent chose no approval categories.
	if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	create := func(schedule string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/crons",
			strings.NewReader(`{"app_id":"`+app.ID+`","schedule":"`+schedule+`","path":"/tick"}`))
		r.Header.Set("Authorization", "Bearer "+plaintext)
		r.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, r)
		return rec
	}

	// A daily cron wakes the 2 GB app for a minute a day: within Pro's $25.
	if rec := create("0 3 * * *"); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("daily cron = %d %s", rec.Code, rec.Body)
	}
	// Every minute keeps it up all month, about $73: past the budget.
	rec := create("* * * * *")
	var problem api.Problem
	if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil ||
		problem.AgentApproval == nil || problem.AgentApproval.Category != api.AgentApprovalBudget {
		t.Fatalf("every-minute cron = %d %s; want 428 budget", rec.Code, rec.Body)
	}
	crons, err := store.ListCronsForApp(ctx, app.ID)
	if err != nil || len(crons) != 1 {
		t.Fatalf("crons after the refused create = %d, %v; want only the daily one", len(crons), err)
	}
}
