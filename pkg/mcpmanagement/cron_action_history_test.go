// adr: 20261105161908
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCronActionHistoryAfterRestartAndExpiry(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		deletion, lost, legacy bool
	}{
		{"update_lost", false, true, false}, {"delete_lost", true, true, false},
		{"update_accepted", false, false, false}, {"delete_accepted", true, false, false},
		{"legacy_delete_receipt", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalFixture(t, true, "release-base")
			cron := api.CronResponse{ID: "cron-one", AppID: f.app.ID, Schedule: "*/15 * * * *", Path: "/PRIVATE_OLD_PATH", Enabled: true, Timezone: "UTC"}
			store, path, key := testStateStore(t)
			var writes atomic.Int32
			var mu sync.Mutex
			var planID string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/crons/cron-one" {
					f.serveHTTP(w, r)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(cron)
					return
				}
				writes.Add(1)
				var saved actionHistorySnapshot
				if err := store.read(cronActionsNamespace+"cron-test", &saved); err != nil || saved.Items[planID].Apply.State != "outcome_unknown" {
					t.Errorf("attempt not retained before native write: %v", err)
				}
				if tc.lost {
					api.WriteProblem(w, api.NewProblem(502, "transport_error", "lost response", "inspect"))
					return
				}
				if tc.deletion {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				cron.Path = "/PRIVATE_NEW_PATH"
				_ = json.NewEncoder(w).Encode(cron)
			}))
			defer backend.Close()
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetCore, ToolsetProvision}, planStore: store, planNamespace: "cron-test"}
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			nextPath := "/PRIVATE_NEW_PATH"
			args := cronChangeArgs{App: "alpha", CronID: cron.ID, Delete: tc.deletion, Reason: "PRIVATE_REASON"}
			if !tc.deletion {
				args.Path = &nextPath
			}
			value, err := m.planCronChange(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			planID = value.(releasePlan).ID
			m.plans[planID].ExpiresAt = time.Now().Add(2 * time.Second)
			if tc.legacy {
				m.plans[planID].Operation.CronHistorySafe = false
			}
			_, err = m.applyOperation(t.Context(), applyArgs{PlanID: planID})
			if (err != nil) != tc.lost {
				t.Fatalf("apply error=%v", err)
			}
			deadline := m.plans[planID].ExpiresAt
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			encrypted, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"PRIVATE_REASON", "/PRIVATE_OLD_PATH", "/PRIVATE_NEW_PATH"} {
				if strings.Contains(string(encrypted), secret) {
					t.Fatal("private plan text written in plaintext")
				}
			}
			if delay := time.Until(deadline) + 10*time.Millisecond; delay > 0 {
				time.Sleep(delay)
			}
			reopened, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			opts.planStore, opts.AllowWrites, opts.Toolsets = reopened, false, []string{ToolsetProvision}
			cs, ctx := connectSelectedTestServer(t, backend, opts, "2025-11-25")
			callTool(t, ctx, cs, "gregale_list_actions", listActionsArgs{App: "alpha"}, false)
			out := callTool(t, ctx, cs, "gregale_get_action", getActionArgs{App: "alpha", PlanID: planID}, false)
			var action actionView
			want := "accepted"
			if tc.lost || tc.legacy {
				want = "outcome_unknown"
			}
			if json.Unmarshal(out["action"], &action) != nil || action.Apply.State != want || action.Cron == nil || action.Cron.ID != "cron-one" || action.PlanAvailable || action.RetryTool != "" || action.Recovery != nil {
				t.Fatalf("history=%s", out)
			}
			body, _ := json.Marshal(out)
			for _, secret := range []string{"PRIVATE_REASON", "/PRIVATE_OLD_PATH", "/PRIVATE_NEW_PATH"} {
				if strings.Contains(string(body), secret) {
					t.Fatal("history exposed cron configuration")
				}
			}
			if writes.Load() != 1 {
				t.Fatalf("history repeated mutation: %d", writes.Load())
			}
		})
	}
}

func TestCronActionHistoryRejectsInvalidBinding(t *testing.T) {
	for _, entry := range []actionRecord{
		{Action: "operation_cron_delete"},
		{Action: "operation_cron_update", Cron: &actionCron{ID: "../wrong"}},
		{Action: "deploy", Cron: &actionCron{ID: "cron-one"}},
		{Action: "operation_cron_delete", Cron: &actionCron{ID: "cron-one"}, Scope: "default", Apply: actionPhase{State: "accepted", Receipt: &actionReceipt{DeploymentID: "unexpected"}}},
	} {
		if validateCronAction(entry) == nil {
			t.Fatalf("accepted invalid action: %+v", entry)
		}
	}
}

func TestCronActionHistoryGrantRevocation(t *testing.T) {
	store, _, _ := testStateStore(t)
	history := actionHistorySnapshot{Version: 1, Items: map[string]actionRecord{}}
	if err := store.updateMany(map[string]any{cronActionsNamespace + "revoked": history, cronActionsNamespace + "active": history}); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"active": {}}}); err != nil {
		t.Fatal(err)
	}
	var revoked, active actionHistorySnapshot
	if err := store.read(cronActionsNamespace+"revoked", &revoked); err != nil || revoked.Version != 0 {
		t.Fatal("revoked cron history retained")
	}
	if err := store.read(cronActionsNamespace+"active", &active); err != nil || active.Version != 1 {
		t.Fatal("active cron history removed")
	}
}

func TestCronActionHistoryKeepsHeadlessStateOnlyDuringRetention(t *testing.T) {
	now := time.Now().UTC()
	history, err := json.Marshal(actionHistorySnapshot{Version: 1, Items: map[string]actionRecord{
		"plan": {RetainedUntil: now.Add(time.Hour)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	records := map[string]json.RawMessage{cronActionsNamespace + "agent": history}
	if !retainedAgentState(records, "agent", now) {
		t.Fatal("cron history did not retain headless agent state")
	}
	if retainedAgentState(records, "another-agent", now) {
		t.Fatal("cron history retained another agent's state")
	}
	if retainedAgentState(records, "agent", now.Add(time.Hour)) {
		t.Fatal("expired cron history retained headless agent state")
	}
}
