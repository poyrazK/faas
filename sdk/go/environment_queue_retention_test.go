package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestEnvironmentGitOpsDefinitionPreservesReviewedQueueDisposition(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	var definition faas.EnvironmentDefinition
	if err := json.Unmarshal([]byte(`{"api_version":"gregale.dev/environment/v1","project":"shop","environment":"production","queue_pruning_policy":"retain","workloads":{"api":{"queue_bindings":{"orders":{"queue_name":"orders","workload_class":"worker"}},"queue_recoveries":{"orders":"`+id+`"}}}}`), &definition); err != nil {
		t.Fatal(err)
	}
	if definition.QueuePruningPolicy != "retain" || definition.Workloads["api"].QueueRecoveries["orders"] != id {
		t.Fatal("SDK dropped reviewed disposition or original identity")
	}
	raw, err := json.Marshal(definition)
	if err != nil || !strings.Contains(string(raw), `"queue_pruning_policy":"retain"`) || !strings.Contains(string(raw), id) {
		t.Fatalf("SDK review round trip: %s %v", raw, err)
	}
}

func TestQueueBindingRecoveryHistoryTransport(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	retired := time.Date(2026, 10, 1, 22, 50, 12, 0, time.UTC)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/apps/worker/queue-bindings" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("history scope/auth: %s", r.URL)
		}
		if calls == 1 && r.URL.RawQuery != "" || calls == 2 && r.URL.Query().Get("include_retired") != "true" {
			t.Errorf("history selector: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_ = json.NewEncoder(w).Encode([]faas.QueueBindingResponse{{ID: id, Environment: "production", RetiredAt: &retired}})
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := c.ListQueueBindings(context.Background(), "worker"); err != nil || len(rows) != 0 {
		t.Fatalf("default list: %+v %v", rows, err)
	}
	rows, err := c.ListQueueBindingHistory(context.Background(), "worker")
	if err != nil || len(rows) != 1 || rows[0].ID != id || rows[0].RetiredAt == nil || !rows[0].RetiredAt.Equal(retired) {
		t.Fatalf("recovery history: %+v %v", rows, err)
	}
}
