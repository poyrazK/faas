// adr: 20261026090000
package api

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRollbackAttemptClientAndHash(t *testing.T) {
	target, current := uuid.NewString(), uuid.NewString()
	req := RollbackRequest{TargetDeploymentID: &target, ExpectedCurrentDeploymentID: &current, Reason: "original"}
	hash := RollbackRequestHash("app", req)
	compact := strings.ReplaceAll(target, "-", "")
	req.TargetDeploymentID = &compact
	if RollbackRequestHash("app", req) != hash {
		t.Fatal("UUID spelling changed request hash")
	}
	req.Reason = "changed"
	if RollbackRequestHash("app", req) == hash {
		t.Fatal("reason omitted from binding")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if r.Header.Get(RollbackAttemptHeader) != strings.Repeat("a", 32) {
				t.Error("attempt missing")
			}
			_ = json.NewEncoder(w).Encode(DeploymentResponse{ID: target})
		} else {
			if r.Header.Get("Idempotency-Key") != "" || r.Header.Get(RollbackAttemptHeader) != "" {
				t.Error("write key leaked")
			}
			_ = json.NewEncoder(w).Encode(RollbackOperation{ID: target})
		}
	}))
	defer server.Close()
	c := NewClient(server.URL, "token")
	ctx := ContextWithIdempotencyKey(ContextWithRollbackAttempt(t.Context(), strings.Repeat("a", 32)), "private-key")
	if _, err := c.CheckedRollback(ctx, "alpha", req); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetRollbackOperation(ctx, "alpha", target); err != nil {
		t.Fatal(err)
	}
}
