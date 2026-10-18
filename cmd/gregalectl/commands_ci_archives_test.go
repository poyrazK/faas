package main

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestCIArchiveCLIRequestsPreserveExecutionScope(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct{ command, method, path string }{
		{"archive", http.MethodPost, "/v1/internal/ci/executions/" + id + "/archive"},
		{"archives", http.MethodGet, "/v1/internal/ci/archives"},
		{"archive-status", http.MethodGet, "/v1/internal/ci/archives/" + id},
	} {
		method, path, _, _, err := ciExecutionRequest(tc.command, ciExecutionOptions{id: id})
		if err != nil || method != tc.method || path != tc.path {
			t.Fatalf("archive command %s: %s %s %v", tc.command, method, path, err)
		}
	}
	for _, command := range []string{"archive", "archive-status"} {
		if _, _, _, _, err := ciExecutionRequest(command, ciExecutionOptions{id: "../outside"}); err == nil {
			t.Fatal("unsafe archive identity accepted")
		}
	}
}
