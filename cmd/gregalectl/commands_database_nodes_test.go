// adr: 1027 — an operator moves a native database through a durable
// operator intent: gregalectl posts it and polls it to a terminal status.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const testDatabaseID = "0f6f3a52-6c0e-4c55-8e0a-2a0b9d6c1d11"

func databaseMoveServer(t *testing.T, status, errMsg string) *httptest.Server {
	t.Helper()
	const intentID = "22222222-2222-2222-2222-222222222222"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/ops/databases/"+testDatabaseID+"/move":
			q := r.URL.Query()
			if q.Get("confirm") != "true" || q.Get("node") != "db-2" || q.Get("reason") != "rebalance" || r.Header.Get("Idempotency-Key") == "" {
				t.Errorf("move request %s, idempotency key %q", r.URL.RawQuery, r.Header.Get("Idempotency-Key"))
			}
			writeTestJSON(w, http.StatusAccepted, api.ObsDatabaseMoveResponse{OK: true, IntentID: intentID,
				StatusURL: "/v1/admin/operator-intents/" + intentID, Kind: "database_move", DatabaseID: testDatabaseID, TargetNode: "db-2"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/operator-intents/"+intentID:
			writeTestJSON(w, http.StatusOK, api.OperatorIntentResponse{IntentID: intentID, Status: status, Error: errMsg})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestDatabaseNodesMoveRecordsTheMove(t *testing.T) {
	server := databaseMoveServer(t, "succeeded", "")
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdDatabaseNodes([]string{"move", "--database", testDatabaseID, "--node", "db-2", "--reason", "rebalance", "--timeout", "5s"}); code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "move of "+testDatabaseID+" recorded") {
		t.Fatalf("stdout = %q", out.String())
	}

	out.Reset()
	jsonOutput = true
	if code := cmdDatabaseNodes([]string{"move", "--database", testDatabaseID, "--node", "db-2", "--reason", "rebalance"}); code != 0 {
		t.Fatalf("json exit = %d, stderr=%s", code, stderr.String())
	}
	var got struct {
		Operation api.ObsDatabaseMoveResponse `json:"operation"`
		Outcome   api.OperatorIntentResponse  `json:"outcome"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Operation.TargetNode != "db-2" || got.Outcome.Status != "succeeded" {
		t.Fatalf("json = %s (%v)", out.String(), err)
	}
}

func TestDatabaseNodesMoveReportsARefusal(t *testing.T) {
	server := databaseMoveServer(t, "failed", "database_move: dbsched: no database node has capacity")
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := moveDatabaseViaAPI(testDatabaseID, "db-2", "rebalance", 5*time.Second); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "no database node has capacity") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if code := cmdDatabaseNodes([]string{"move"}); code != 2 {
		t.Fatalf("move without --database exit = %d, want 2", code)
	}
}

func databaseRestartServer(t *testing.T, status, errMsg string) *httptest.Server {
	t.Helper()
	const intentID = "33333333-3333-3333-3333-333333333333"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/ops/databases/"+testDatabaseID+"/restart":
			q := r.URL.Query()
			if q.Get("confirm") != "true" || q.Get("reason") != "minor_update" || r.Header.Get("Idempotency-Key") == "" {
				t.Errorf("restart request %s, idempotency key %q", r.URL.RawQuery, r.Header.Get("Idempotency-Key"))
			}
			writeTestJSON(w, http.StatusAccepted, api.ObsDatabaseRestartResponse{OK: true, IntentID: intentID,
				StatusURL: "/v1/admin/operator-intents/" + intentID, Kind: "database_restart", DatabaseID: testDatabaseID})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/operator-intents/"+intentID:
			writeTestJSON(w, http.StatusOK, api.OperatorIntentResponse{IntentID: intentID, Status: status, Error: errMsg})
		default:
			http.NotFound(w, r)
		}
	}))
}

// An operator restarts a native database in place through a durable
// intent, and sees a refusal when schedd cannot.
func TestDatabaseNodesRestart(t *testing.T) {
	server := databaseRestartServer(t, "succeeded", "")
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdDatabaseNodes([]string{"restart", "--database", testDatabaseID, "--reason", "minor_update", "--timeout", "5s"}); code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "restarted "+testDatabaseID) {
		t.Fatalf("stdout = %q", out.String())
	}
	if code := cmdDatabaseNodes([]string{"restart"}); code != 2 {
		t.Fatalf("restart without --database exit = %d, want 2", code)
	}

	refused := databaseRestartServer(t, "failed", "database_restart: dbsched: conflict: the database is moving")
	defer refused.Close()
	installTestOperatorSession(t, refused.URL, "opaque-session")
	stderr.Reset()
	if code := restartDatabaseViaAPI(testDatabaseID, "minor_update", 5*time.Second); code != 1 || !strings.Contains(stderr.String(), "the database is moving") {
		t.Fatalf("refused restart exit = %d, stderr = %q", code, stderr.String())
	}
}

// An operator switches a production database over to its standby through
// a durable intent, and sees a refusal when it has no synchronized one.
func TestDatabaseNodesSwitchover(t *testing.T) {
	const intentID = "44444444-4444-4444-4444-444444444444"
	serve := func(status, errMsg string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/ops/databases/"+testDatabaseID+"/switchover":
				if q := r.URL.Query(); q.Get("confirm") != "true" || q.Get("reason") != "node_maintenance" || r.Header.Get("Idempotency-Key") == "" {
					t.Errorf("switchover request %s, idempotency key %q", r.URL.RawQuery, r.Header.Get("Idempotency-Key"))
				}
				writeTestJSON(w, http.StatusAccepted, api.ObsDatabaseSwitchoverResponse{OK: true, IntentID: intentID,
					StatusURL: "/v1/admin/operator-intents/" + intentID, Kind: "database_switchover", DatabaseID: testDatabaseID})
			case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/operator-intents/"+intentID:
				writeTestJSON(w, http.StatusOK, api.OperatorIntentResponse{IntentID: intentID, Status: status, Error: errMsg})
			default:
				http.NotFound(w, r)
			}
		}))
	}
	server := serve("succeeded", "")
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdDatabaseNodes([]string{"switchover", "--database", testDatabaseID, "--reason", "node_maintenance", "--timeout", "5s"}); code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "switched "+testDatabaseID+" over") {
		t.Fatalf("stdout = %q", out.String())
	}
	if code := cmdDatabaseNodes([]string{"switchover"}); code != 2 {
		t.Fatalf("switchover without --database exit = %d, want 2", code)
	}
	refused := serve("failed", "database_switchover: dbsched: conflict: no synchronized standby to switch over to")
	defer refused.Close()
	installTestOperatorSession(t, refused.URL, "opaque-session")
	stderr.Reset()
	if code := switchoverDatabaseViaAPI(testDatabaseID, "node_maintenance", 5*time.Second); code != 1 || !strings.Contains(stderr.String(), "no synchronized standby") {
		t.Fatalf("refused switchover exit = %d, stderr = %q", code, stderr.String())
	}
}
