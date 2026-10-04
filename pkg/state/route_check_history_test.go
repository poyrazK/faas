package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func historyFixture(t *testing.T, store state.Store) (state.Account, state.App, api.RoutePolicyApplyRequest) {
	t.Helper()
	account, app, request, intent, saved := savedCheckFixture(t, store)
	if err := store.MarkDeploymentLive(t.Context(), request.DeploymentID); err != nil {
		t.Fatal(err)
	}
	max := int64(500)
	request.Requirements.Groups = append(request.Requirements.Groups, api.RouteGroup{Name: "billing", PathPrefix: "/billing/", Methods: []string{"POST"}, Require: api.RouteChecks{Budget: &api.RouteBudgetRequirement{Explicit: true, MaxMS: &max}}})
	if _, err := intent.SaveRouteRequirements(t.Context(), account.ID, app.ID, api.SaveRouteRequirementsRequest{Requirements: request.Requirements, ExpectedRevision: &saved.Revision}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, account.ID, app.ID, historyContract(), "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	monitorApply(t, store, account, app, request, "history-initial")
	monitorComplete(t, store, "satisfied")
	return account, app, request
}

func TestRouteCheckHistoryAtomicRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, app, request := historyFixture(t, store)
	monitorHook(t, store, account.ID, app.ID, "rollback", []string{"routes.requirements.changed"}, true)
	worsenHistoryBudget(t, store, app.ID, "/checkout/")
	first := monitorComplete(t, store, "violated")
	worsenHistoryBudget(t, store, app.ID, "/billing/")
	claim, err := store.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
	if err != nil {
		t.Fatal(err)
	}
	check, err := store.CheckRouteRequirements(t.Context(), account.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: request.DeploymentID}, testSavedRequirementsChecker(request.DeploymentID))
	if err != nil {
		t.Fatal(err)
	}
	// Reject after the completion update has already run its notification
	// triggers. No result, baseline or outbox write may survive that failure.
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_history_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced history failure'; END; $$;
		CREATE TRIGGER reject_history_test BEFORE INSERT ON route_check_history FOR EACH ROW EXECUTE FUNCTION reject_history_test()`); err != nil {
		t.Fatal(err)
	}
	if done, err := store.CompleteAutomaticRouteCheck(t.Context(), claim, check, check.Report.Coverage.SHA256, false); err == nil || done {
		t.Fatalf("history failure committed: %v %v", done, err)
	}
	latest, err := store.GetAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID, automaticCheckFingerprint)
	if err != nil || latest.CheckID != first.RequestID || latest.State != "running" {
		t.Fatalf("history rollback changed latest result: %+v %v", latest, err)
	}
	if _, err := store.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, claim.RequestID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed completion retained evidence")
	}
	monitorPendingEvents(t, store, 0)
	if _, err := pool.Exec(t.Context(), "DROP TRIGGER reject_history_test ON route_check_history; DROP FUNCTION reject_history_test()"); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	if done, err := restarted.CompleteAutomaticRouteCheck(t.Context(), claim, check, check.Report.Coverage.SHA256, false); err != nil || !done {
		t.Fatalf("retry lost claim: %v %v", done, err)
	}
	entry, err := restarted.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, claim.RequestID)
	if err != nil || entry.Changes.Summary.NewlyViolated != 1 || entry.Changes.Findings[0].BeforeCheckID != first.RequestID {
		t.Fatalf("rollback advanced finding baseline: %+v %v", entry.Changes, err)
	}
	monitorPendingEvents(t, restarted, 1)
}

func TestRouteCheckHistoryLeaseExpiresWhileWaiting(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, app, request := historyFixture(t, store)
	before, err := store.GetAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID, automaticCheckFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimAutomaticRouteCheck(t.Context(), 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	check, err := store.CheckRouteRequirements(t.Context(), account.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: request.DeploymentID}, testSavedRequirementsChecker(request.DeploymentID))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id = $1 FOR UPDATE", account.ID); err != nil {
		t.Fatal(err)
	}
	type completion struct {
		done bool
		err  error
	}
	finished := make(chan completion, 1)
	go func() {
		done, err := store.CompleteAutomaticRouteCheck(t.Context(), claim, check, check.Report.Coverage.SHA256, false)
		finished <- completion{done, err}
	}()
	// PostgreSQL now() is the transaction start time. The fencing checks must
	// use wall time after waiting for parent locks, or this expired lease wins.
	select {
	case result := <-finished:
		t.Fatalf("completion crossed a parent lock: %+v", result)
	case <-time.After(time.Until(claim.LeaseUntil.Add(100 * time.Millisecond))):
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-finished:
		if result.err != nil || result.done {
			t.Fatalf("expired lease retained evidence: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion stalled after releasing parent lock")
	}
	latest, err := store.GetAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID, automaticCheckFingerprint)
	if err != nil || latest.CheckID != before.CheckID {
		t.Fatalf("expired lease replaced latest check: %+v %v", latest, err)
	}
	if _, err := store.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, claim.RequestID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("expired completion wrote history")
	}
}

func TestRouteCheckHistoryByteRetentionAndStaleLease(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			account, app, request := historyFixture(t, store)
			queue := store.(state.AutomaticRouteCheckStore)
			history := store.(state.RouteCheckHistoryStore)
			if err := queue.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
				t.Fatal(err)
			}
			old, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			if err != nil {
				t.Fatal(err)
			}
			worsenHistoryBudget(t, store, app.ID, "/checkout/")
			if finishAutomaticCheck(t, store, old) {
				t.Fatal("superseded worker retained history")
			}
			if _, err := history.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, old.RequestID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("stale lease created history")
			}
			var firstID, latestID string
			for i := 0; i < 6; i++ {
				if i > 0 {
					if err := queue.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
						t.Fatal(err)
					}
				}
				claim, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
				if err != nil {
					t.Fatal(err)
				}
				check, err := store.(state.RouteRequirementsStore).CheckRouteRequirements(t.Context(), account.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: request.DeploymentID}, testSavedRequirementsChecker(request.DeploymentID))
				if err != nil {
					t.Fatal(err)
				}
				// Exercise storage byte limits with a large allowed result shape.
				check.Report.Routes[0].Checks[0].Reason = strings.Repeat("x", 14<<20)
				if done, err := queue.CompleteAutomaticRouteCheck(t.Context(), claim, check, check.Report.Coverage.SHA256, false); err != nil || !done {
					t.Fatalf("large completion: %v %v", done, err)
				}
				if i == 0 {
					firstID = claim.RequestID
				}
				latestID = claim.RequestID
			}
			page, err := history.ListRouteCheckHistory(t.Context(), account.ID, app.ID, request.DeploymentID, api.RouteCheckHistoryMaxPage, "")
			if err != nil || len(page.Entries) != 4 || page.Entries[0].ID != latestID {
				t.Fatalf("byte retention: entries=%d %v", len(page.Entries), err)
			}
			encoded := 0
			for _, summary := range page.Entries {
				entry, err := history.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, summary.ID)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(entry)
				encoded += len(body)
			}
			if encoded > api.RouteCheckHistoryMaxBytes {
				t.Fatal("history exceeds encoded byte budget")
			}
			if _, err := history.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, firstID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("byte retention kept expired evidence")
			}
		})
	}
}

func historyContract() []byte {
	return []byte(`{"openapi":"3.1.0","paths":{"/checkout/{id}":{"post":{}},"/billing/{id}":{"post":{}},"/health":{"get":{}}}}`)
}

func worsenHistoryBudget(t *testing.T, store state.Store, appID, path string) {
	t.Helper()
	rules, _ := store.ListEdgeRulesForApp(t.Context(), appID)
	for _, rule := range rules {
		if rule.Kind == state.EdgeRuleKindBudget && strings.HasPrefix(rule.MatchPath, path) {
			action := state.EdgeRuleAction{Budget: &state.EdgeRuleBudgetAction{BudgetMs: 2000}}
			if _, err := store.UpdateEdgeRule(t.Context(), rule.ID, state.UpdateEdgeRuleParams{Action: &action}); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing budget rule")
}

func TestRouteCheckHistoryNewViolationDuringIncident(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			account, app, request := historyFixture(t, store)
			queue := store.(state.AutomaticRouteCheckStore)
			history := store.(state.RouteCheckHistoryStore)
			hook := monitorHook(t, store, account.ID, app.ID, "changes", []string{"routes.requirements.changed"}, true)
			worsenHistoryBudget(t, store, app.ID, "/checkout/")
			first := monitorComplete(t, store, "violated")
			monitorPendingEvents(t, store, 0)
			worsenHistoryBudget(t, store, app.ID, "/billing/")
			second := monitorComplete(t, store, "violated")
			monitorPendingEvents(t, store, 1)
			entry, err := history.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, second.RequestID)
			if err != nil || entry.Changes.Summary.NewlyViolated != 1 || entry.Changes.Status != "comparable" || entry.Check.Report.Status != "violated" {
				t.Fatalf("new violation history: %+v %v", entry, err)
			}
			if len(entry.Changes.Findings) != 1 || entry.Changes.Findings[0].Path != "/billing/{id}" || entry.Changes.Findings[0].BeforeCheckID != first.RequestID {
				t.Fatalf("finding delta: %+v", entry.Changes)
			}
			if n, err := store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
				t.Fatalf("change relay: %d %v", n, err)
			}
			deliveries, _, _ := store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
			if len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventRouteRequirementsChanged {
				t.Fatalf("new violation event: %+v", deliveries)
			}
			var payload map[string]json.RawMessage
			_ = json.Unmarshal(deliveries[0].Payload, &payload)
			if !strings.Contains(string(payload["history_path"]), second.RequestID) || strings.Contains(string(deliveries[0].Payload), "/billing") || strings.Contains(string(deliveries[0].Payload), "private") {
				t.Fatal("notification lacks exact evidence or leaks finding detail")
			}
			// Duplicate delivery/completion cannot add history or notification.
			if done := finishAutomaticCheck(t, store, second); done {
				t.Fatal("already completed claim was accepted")
			}
			if err := queue.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
				t.Fatal(err)
			}
			monitorComplete(t, store, "violated")
			monitorPendingEvents(t, store, 0)
			if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, account.ID); err != nil {
				t.Fatal(err)
			}
			// Unknown history can evict the old known snapshot without losing the
			// independent finding baseline or generating a repeat alert.
			for i := 0; i <= api.RouteCheckHistoryMaxEntries; i++ {
				if i > 0 {
					if err := queue.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
						t.Fatal(err)
					}
				}
				monitorComplete(t, store, "unknown")
			}
			if _, err := history.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, second.RequestID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("old history did not expire: %v", err)
			}
			if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, account.ID, app.ID, historyContract(), "manual_upload", false); err != nil {
				t.Fatal(err)
			}
			monitorComplete(t, store, "violated")
			monitorPendingEvents(t, store, 0)
			page, err := history.ListRouteCheckHistory(t.Context(), account.ID, app.ID, request.DeploymentID, 3, "")
			if err != nil || len(page.Entries) != 3 || page.NextCursor == "" || page.Entries[0].Summary.NewlyViolated != 0 {
				t.Fatalf("retained page: %+v %v", page, err)
			}
			next, err := history.ListRouteCheckHistory(t.Context(), account.ID, app.ID, request.DeploymentID, 3, page.NextCursor)
			if err != nil || len(next.Entries) != 3 || next.Entries[0].ID == page.Entries[2].ID {
				t.Fatalf("pagination: %+v %v", next, err)
			}
			if _, err := history.ListRouteCheckHistory(t.Context(), account.ID, app.ID, request.DeploymentID, 3, second.RequestID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("expired cursor did not fail explicitly")
			}
			// Returned evidence is isolated from retained storage.
			page.Entries[0].Status = "mutated"
			read, _ := history.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, request.DeploymentID, page.Entries[0].ID)
			if read.Check.Report.Status == "mutated" {
				t.Fatal("history aliases caller memory")
			}
			foreign, _ := store.CreateAccount(t.Context(), "history-foreign@example.test", api.PlanPro)
			if _, err := history.ListRouteCheckHistory(t.Context(), foreign.ID, app.ID, request.DeploymentID, 3, ""); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("history crossed tenant ownership")
			}
			if _, err := history.GetRouteCheckHistoryEntry(t.Context(), account.ID, app.ID, uuid.NewString(), read.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("history crossed deployment ownership")
			}
			if err := store.UpdateAccountPlan(t.Context(), account.ID, api.PlanFree); err != nil {
				t.Fatal(err)
			}
			if _, err := history.ListRouteCheckHistory(t.Context(), account.ID, app.ID, request.DeploymentID, 3, ""); !errors.Is(err, state.ErrAutomaticRouteCheckPlan) {
				t.Fatal("downgrade exposed retained route inventory")
			}
		})
	}
}
