// adr: 1027 — the operator's database move only records a durable
// database_move intent after validating confirm, id, node and reason.
package managedpg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordedIntent struct {
	kind             state.OperatorIntentKind
	target, actor    string
	reason           string
	metadata         json.RawMessage
	account, traceID *string
}

type fakeIntents struct{ got []recordedIntent }

func (f *fakeIntents) InsertOperatorIntent(_ context.Context, kind state.OperatorIntentKind, target string, account *string, actor, reason string, metadata json.RawMessage, traceID *string) (string, error) {
	f.got = append(f.got, recordedIntent{kind, target, actor, reason, metadata, account, traceID})
	return "33333333-3333-3333-3333-333333333333", nil
}

type fakeNotifier struct{ channels []string }

func (f *fakeNotifier) Notify(_ context.Context, channel, _ string) error {
	f.channels = append(f.channels, channel)
	return nil
}

type fakeAudit struct{ kinds []string }

func (f *fakeAudit) Emit(_ context.Context, kind string, _ *string, _ map[string]any) {
	f.kinds = append(f.kinds, kind)
}

const databaseID = "0f6f3a52-6c0e-4c55-8e0a-2a0b9d6c1d11"

func serveMove(m Move, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, nil)
	r.SetPathValue("id", r.URL.Query().Get("_id"))
	w := httptest.NewRecorder()
	m.Post(w, r, state.Account{ID: "operator-1"})
	return w
}

func TestMoveRecordsADatabaseMoveIntent(t *testing.T) {
	intents, notifier, audit := &fakeIntents{}, &fakeNotifier{}, &fakeAudit{}
	m := Move{AdminAllows: func(state.Account) (bool, *api.Problem) { return true, nil }, Intents: intents, Notifier: notifier, Audit: audit}
	w := serveMove(m, "/move?confirm=true&node=db-2&reason=rebalance&_id="+databaseID)
	if w.Code != http.StatusAccepted || len(intents.got) != 1 {
		t.Fatalf("status %d, intents %d: %s", w.Code, len(intents.got), w.Body.String())
	}
	got := intents.got[0]
	if got.kind != state.OperatorIntentKindDatabaseMove || got.target != databaseID || got.actor != "operator-1" || got.reason != "rebalance" || got.account != nil {
		t.Fatalf("intent = %+v", got)
	}
	var meta state.DatabaseMoveIntentMetadata
	if err := json.Unmarshal(got.metadata, &meta); err != nil || meta.TargetNode != "db-2" {
		t.Fatalf("metadata = %s (%v)", got.metadata, err)
	}
	var resp api.ObsDatabaseMoveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.StatusURL != "/v1/admin/operator-intents/33333333-3333-3333-3333-333333333333" || resp.TargetNode != "db-2" {
		t.Fatalf("response = %s (%v)", w.Body.String(), err)
	}
	if len(notifier.channels) != 1 || len(audit.kinds) != 1 || audit.kinds[0] != "operator.action.database_move" {
		t.Fatalf("notify %v, audit %v", notifier.channels, audit.kinds)
	}

	// No node lets schedd choose; no reason takes the default.
	w = serveMove(m, "/move?confirm=true&_id="+databaseID)
	if w.Code != http.StatusAccepted || intents.got[1].reason != "operator_database_move" || string(intents.got[1].metadata) != "{}" {
		t.Fatalf("defaults: status %d, intent %+v", w.Code, intents.got[1])
	}
}

func TestMoveRefusesBeforeRecording(t *testing.T) {
	intents := &fakeIntents{}
	allow := func(state.Account) (bool, *api.Problem) { return true, nil }
	m := Move{AdminAllows: allow, Intents: intents, Notifier: &fakeNotifier{}}
	for name, tc := range map[string]struct {
		target string
		want   int
	}{
		"no confirm": {"/move?_id=" + databaseID, http.StatusBadRequest},
		"bad id":     {"/move?confirm=true&_id=not-a-uuid", http.StatusBadRequest},
		"bad node":   {"/move?confirm=true&node=DB_2&_id=" + databaseID, http.StatusBadRequest},
		"bad reason": {"/move?confirm=true&reason=Re+Balance&_id=" + databaseID, http.StatusBadRequest},
	} {
		if w := serveMove(m, tc.target); w.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, w.Code, tc.want)
		}
	}
	m.AdminAllows = func(state.Account) (bool, *api.Problem) {
		return false, api.NewProblem(http.StatusForbidden, "admin_required", "Admin access required", "")
	}
	if w := serveMove(m, "/move?confirm=true&_id="+databaseID); w.Code != http.StatusForbidden {
		t.Errorf("non-admin: status %d", w.Code)
	}
	if len(intents.got) != 0 {
		t.Fatalf("refused requests recorded %d intents", len(intents.got))
	}
}
