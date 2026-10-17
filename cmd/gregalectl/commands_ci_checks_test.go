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

func TestCISourceCheckCLIRequestsKeepBindingAndEventScope(t *testing.T) {
	binding, event := uuid.NewString(), uuid.NewString()
	base := "/v1/internal/ci/github/automation/" + binding
	for _, command := range []string{"list", "status", "retry"} {
		id, key, generation := event, "", int64(-1)
		want := base + "/events/" + event + "/check"
		verb := http.MethodGet
		if command == "list" {
			id = ""
			want = base + "/checks"
		}
		if command == "retry" {
			key = "stable-source-key"
			generation = 1
			want += "/retry"
			verb = http.MethodPost
		}
		method, path, body, headers, err := ciChecksScopedRequest(command, "", binding, id, key, generation)
		if err != nil || method != verb || path != want {
			t.Fatalf("source %s: %s %s %v", command, method, path, err)
		}
		if command == "retry" && (headers.Get("Idempotency-Key") != key || body.(ci.CheckRetryRequest).ExpectedRetryGeneration != 1) {
			t.Fatal("source recovery lost request identity")
		}
	}
	for _, tc := range []struct {
		command, id, binding, event, key string
		generation                       int64
	}{
		{"status", event, binding, event, "", -1}, {"retry", event, binding, event, "key", 0},
		{"status", "", "", event, "", -1}, {"status", "", binding, "", "", -1}, {"status", "", "../outside", event, "", -1},
		{"list", "", binding, event, "", -1}, {"list", "", binding, "", "key", -1}, {"list", "", binding, "", "", 0},
		{"retry", "", binding, event, "", 0}, {"retry", "", binding, event, "key", -1}, {"retry", "", binding, event, "key", 4},
		{"status", "", binding, event, "key", -1}, {"status", "", binding, event, "", 0}, {"unknown", "", binding, event, "", -1},
	} {
		if _, _, _, _, err := ciChecksScopedRequest(tc.command, tc.id, tc.binding, tc.event, tc.key, tc.generation); err == nil {
			t.Fatalf("invalid mixed source request: %+v", tc)
		}
	}
	if method, path, _, _, err := ciChecksScopedRequest("status", event, "", "", "", -1); err != nil || method != http.MethodGet || path != "/v1/internal/ci/checks/"+event {
		t.Fatal("legacy execution scope changed")
	}
}
