package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMigrationRecoveryRequestAndClient(t *testing.T) {
	req := ProjectMigrationRecoveryRequest{RequestID: strings.Repeat("a", 32), ExpectedTaskID: uuid.NewString(), ExpectedGeneration: 3, ReplacementAttemptID: strings.Repeat("b", 32), DatabaseStateChecked: true, ExecutionQuiescent: true, EvidenceHash: strings.Repeat("c", 64)}
	for _, field := range []string{"database", "quiescence", "hash", "task", "generation"} {
		bad := req
		switch field {
		case "database":
			bad.DatabaseStateChecked = false
		case "quiescence":
			bad.ExecutionQuiescent = false
		case "hash":
			bad.EvidenceHash = strings.Repeat("C", 64)
		case "task":
			bad.ExpectedTaskID = "task"
		case "generation":
			bad.ExpectedGeneration = 0
		}
		if bad.Valid() {
			t.Fatalf("accepted invalid %s", field)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-submissions/"+strings.Repeat("d", 32)+"/resolve-contract" || r.Method != http.MethodPost {
			t.Error("wrong recovery endpoint")
		}
		var got ProjectMigrationRecoveryRequest
		if json.NewDecoder(r.Body).Decode(&got) != nil || got != req {
			t.Error("recovery identity changed in transport")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"attempt_id":"` + strings.Repeat("d", 32) + `"}`))
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "").ResolveProjectMigrationContract(context.Background(), strings.Repeat("d", 32), req); err != nil {
		t.Fatal(err)
	}
}
