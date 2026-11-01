package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The record verbs act only on a record of the trigger in the URL. Before
// the fix, POST /v1/triggers/{own}/records/{rid}/retry and /drop authorised
// the caller's own trigger and then retried or deleted {rid} wherever it
// lived, including another account's record (blocker hunt 2026-11-01 #6).
func TestTriggerRecordVerbsAreScopedToTheTrigger(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	newTrigger := func(t *testing.T, accountID, slug string) (string, string) {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", slug, true,
			[]byte(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`), "", 10, 1000, 3, 1024, "commit", api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		return app.ID, trigger.ID.String()
	}
	newRecord := func(t *testing.T, triggerID string) string {
		t.Helper()
		id, err := store.InsertTriggerRecord(ctx, triggerID, "0-"+uuid.NewString(), []byte(`{}`), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkTriggerRecordDeadLetter(ctx, id, "max_attempts"); err != nil {
			t.Fatal(err)
		}
		return id
	}
	recordState := func(t *testing.T, triggerID, recordID string) string {
		t.Helper()
		rows, err := store.ListTriggerRecordsForTrigger(ctx, triggerID, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID.String() == recordID {
				return row.State
			}
		}
		return "missing"
	}

	_, own := newTrigger(t, (*e.Acct()).ID, "own")
	_, sibling := newTrigger(t, (*e.Acct()).ID, "sibling")
	other, err := store.CreateAccount(ctx, "other-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, foreign := newTrigger(t, other.ID, "foreign")

	for _, verb := range []string{"retry", "drop"} {
		t.Run(verb, func(t *testing.T) {
			for name, target := range map[string]string{"sibling trigger": sibling, "another account": foreign} {
				recordID := newRecord(t, target)
				rec := e.Do(t, http.MethodPost, "/v1/triggers/"+own+"/records/"+recordID+"/"+verb, nil, nil)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("%s record through own trigger = %d %s, want 404", name, rec.Code, rec.Body)
				}
				if got := recordState(t, target, recordID); got != "dead_letter" {
					t.Fatalf("%s record changed to %q", name, got)
				}
			}
			foreignRecord := newRecord(t, foreign)
			if rec := e.Do(t, http.MethodPost, "/v1/triggers/"+foreign+"/records/"+foreignRecord+"/"+verb, nil, nil); rec.Code != http.StatusNotFound {
				t.Fatalf("another account's trigger = %d, want 404", rec.Code)
			}
			ownRecord := newRecord(t, own)
			if rec := e.Do(t, http.MethodPost, "/v1/triggers/"+own+"/records/"+ownRecord+"/"+verb, nil, nil); rec.Code != http.StatusNoContent {
				t.Fatalf("own record = %d %s, want 204", rec.Code, rec.Body)
			}
			want := map[string]string{"retry": "pending", "drop": "missing"}[verb]
			if got := recordState(t, own, ownRecord); got != want {
				t.Fatalf("own record after %s = %q, want %q", verb, got, want)
			}
		})
	}
}

// Retry and drop change records, so a key that may only read apps is refused.
func TestTriggerRecordVerbsNeedWriteScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "scoped"})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", "scoped", true,
		[]byte(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`), "", 10, 1000, 3, 1024, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	recordID, err := store.InsertTriggerRecord(ctx, trigger.ID.String(), "0-1", []byte(`{}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, (*e.Acct()).ID, hash, "reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"retry", "drop"} {
		rec := e.DoAs(t, http.MethodPost, "/v1/triggers/"+trigger.ID.String()+"/records/"+recordID+"/"+verb, nil, nil, token)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s with a read-only key = %d %s, want 403", verb, rec.Code, rec.Body)
		}
	}
	if rows, err := store.ListTriggerRecordsForTrigger(ctx, trigger.ID.String(), 10); err != nil || len(rows) != 1 || rows[0].State != "pending" {
		t.Fatalf("read-only key changed the record: %+v %v", rows, err)
	}
}
