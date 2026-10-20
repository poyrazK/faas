// adr: 1027 — the operator's in-place database restart and planned
// switchover only record a durable intent after validating confirm, id
// and reason, and every database operation is served from one
// registration.
package managedpg

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRestartRecordsADatabaseRestartIntent(t *testing.T) {
	intents, notifier, audit := &fakeIntents{}, &fakeNotifier{}, &fakeAudit{}
	rs := Restart{AdminAllows: func(state.Account) (bool, *api.Problem) { return true, nil }, Intents: intents, Notifier: notifier, Audit: audit}
	serve := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, target, nil)
		r.SetPathValue("id", r.URL.Query().Get("_id"))
		w := httptest.NewRecorder()
		rs.Post(w, r, state.Account{ID: "operator-1"})
		return w
	}
	w := serve("/restart?confirm=true&_id=" + databaseID)
	if w.Code != http.StatusAccepted || len(intents.got) != 1 {
		t.Fatalf("status %d, intents %d: %s", w.Code, len(intents.got), w.Body.String())
	}
	got := intents.got[0]
	if got.kind != state.OperatorIntentKindDatabaseRestart || got.target != databaseID || got.actor != "operator-1" ||
		got.reason != "operator_database_restart" || got.account != nil || got.metadata != nil {
		t.Fatalf("intent = %+v", got)
	}
	var resp api.ObsDatabaseRestartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Kind != "database_restart" || resp.DatabaseID != databaseID || !strings.HasSuffix(resp.StatusURL, resp.IntentID) {
		t.Fatalf("response = %+v, %v", resp, err)
	}
	if len(notifier.channels) != 1 || len(audit.kinds) != 1 || audit.kinds[0] != "operator.action.database_restart" {
		t.Fatalf("notify %v, audit %v", notifier.channels, audit.kinds)
	}
	for name, target := range map[string]string{
		"no confirm": "/restart?_id=" + databaseID,
		"bad id":     "/restart?confirm=true&_id=not-a-uuid",
		"bad reason": "/restart?confirm=true&reason=Bad%20Reason&_id=" + databaseID,
	} {
		if w := serve(target); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, w.Code)
		}
	}
	if len(intents.got) != 1 {
		t.Fatalf("a refused request recorded an intent: %d", len(intents.got))
	}
	rs.AdminAllows = func(state.Account) (bool, *api.Problem) {
		return false, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "forbidden", "not an operator")
	}
	if w := serve("/restart?confirm=true&_id=" + databaseID); w.Code != http.StatusForbidden || len(intents.got) != 1 {
		t.Fatalf("a non-operator restart: status %d, intents %d", w.Code, len(intents.got))
	}
}

func TestSwitchoverRecordsADatabaseSwitchoverIntent(t *testing.T) {
	intents, audit := &fakeIntents{}, &fakeAudit{}
	sw := Switchover{AdminAllows: func(state.Account) (bool, *api.Problem) { return true, nil }, Intents: intents, Notifier: &fakeNotifier{}, Audit: audit}
	serve := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, target, nil)
		r.SetPathValue("id", r.URL.Query().Get("_id"))
		w := httptest.NewRecorder()
		sw.Post(w, r, state.Account{ID: "operator-1"})
		return w
	}
	w := serve("/switchover?confirm=true&reason=node_maintenance&_id=" + databaseID)
	if w.Code != http.StatusAccepted || len(intents.got) != 1 {
		t.Fatalf("status %d, intents %d: %s", w.Code, len(intents.got), w.Body.String())
	}
	if got := intents.got[0]; got.kind != state.OperatorIntentKindDatabaseSwitchover || got.target != databaseID || got.reason != "node_maintenance" || got.account != nil {
		t.Fatalf("intent = %+v", got)
	}
	var resp api.ObsDatabaseSwitchoverResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Kind != "database_switchover" || resp.DatabaseID != databaseID {
		t.Fatalf("response = %+v, %v", resp, err)
	}
	if len(audit.kinds) != 1 || audit.kinds[0] != "operator.action.database_switchover" {
		t.Fatalf("audit %v", audit.kinds)
	}
	if w := serve("/switchover?_id=" + databaseID); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "switch over") || len(intents.got) != 1 {
		t.Fatalf("no confirm: status %d, intents %d: %s", w.Code, len(intents.got), w.Body.String())
	}
	if w := serve("/switchover?confirm=true&_id=" + databaseID); w.Code != http.StatusAccepted || intents.got[1].reason != "operator_database_switchover" {
		t.Fatalf("default reason: status %d, intent %+v", w.Code, intents.got)
	}
}

func TestOperationsRegisterServesMoveRestartAndSwitchover(t *testing.T) {
	intents := &fakeIntents{}
	ops := Operations{AdminAllows: func(state.Account) (bool, *api.Problem) { return true, nil }, Intents: intents, Notifier: &fakeNotifier{}}
	mux := http.NewServeMux()
	wrapped := 0
	trace := func(h http.Handler) http.Handler { return h }
	auth := func(h httpkit.AccountHandler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { h(w, r, state.Account{ID: "operator-1"}) }
	}
	mutation := func(h httpkit.AccountHandler) httpkit.AccountHandler { wrapped++; return h }
	ops.Register(mux, trace, auth, mutation)
	for _, path := range []string{"/v1/admin/ops/databases/" + databaseID + "/move?confirm=true", "/v1/admin/ops/databases/" + databaseID + "/restart?confirm=true",
		"/v1/admin/ops/databases/" + databaseID + "/switchover?confirm=true"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s: status %d: %s", path, w.Code, w.Body.String())
		}
	}
	if wrapped != 3 || len(intents.got) != 3 || intents.got[0].kind != state.OperatorIntentKindDatabaseMove || intents.got[1].kind != state.OperatorIntentKindDatabaseRestart ||
		intents.got[2].kind != state.OperatorIntentKindDatabaseSwitchover {
		t.Fatalf("wrapped %d, intents %+v", wrapped, intents.got)
	}
}
