package main

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCICheckRecoveryCLIRequestsKeepScopeAndIdentity(t *testing.T) {
	id := uuid.NewString()
	method, path, body, headers, err := ciChecksRequest("retry", id, "stable-retry-key", 0)
	if err != nil || method != http.MethodPost || path != "/v1/internal/ci/checks/"+id+"/retry" || headers.Get("Idempotency-Key") != "stable-retry-key" || body.(ci.CheckRetryRequest).ExpectedRetryGeneration != 0 {
		t.Fatal("retry lost execution scope or stable request identity")
	}
	for _, tc := range []struct {
		command, id, key string
		generation       int64
	}{
		{"retry", id, "", 0}, {"retry", id, "key", -1}, {"retry", id, "key", 4}, {"retry", "../outside", "key", 0},
		{"status", id, "key", -1}, {"status", id, "", 0}, {"list", id, "", -1}, {"list", "", "key", -1}, {"unknown", id, "", -1},
	} {
		if _, _, _, _, err := ciChecksRequest(tc.command, tc.id, tc.key, tc.generation); err == nil {
			t.Fatalf("invalid request accepted: %+v", tc)
		}
	}
	if method, path, _, _, err := ciChecksRequest("list", "", "", -1); err != nil || method != http.MethodGet || path != "/v1/internal/ci/checks" {
		t.Fatal("list request malformed")
	}
	if method, path, _, _, err := ciChecksRequest("status", id, "", -1); err != nil || method != http.MethodGet || path != "/v1/internal/ci/checks/"+id {
		t.Fatal("status request malformed")
	}
}
