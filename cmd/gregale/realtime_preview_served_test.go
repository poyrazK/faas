package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A preview verb follows the server's capability answer, not only the shell
// variable: the CLI said "unknown realtime subcommand occupancy" to accounts
// gregale_context told the feature was on.
func TestRealtimePreviewVerbFollowsServerCapability(t *testing.T) {
	var occupancyEnabled atomic.Bool
	var occupancyCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(map[string]any{"registry_version": 1, "plan": "pro",
				"capabilities": []any{map[string]any{"key": "realtime-occupancy", "enabled": occupancyEnabled.Load()}}})
		case strings.HasSuffix(r.URL.Path, "/occupancy"):
			occupancyCalls.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			_, _ = w.Write([]byte("[]"))
		}
	}))
	defer server.Close()
	clearCapabilityReleaseAll(t)
	t.Setenv("FAAS_REALTIME_PREVIEW_ACCOUNTS", "")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))

	code, _, stderr := runCLI(t, "realtime", "occupancy", "app", "endpoint", "lobby")
	if code == 0 || !strings.Contains(stderr, "not enabled for this account") || strings.Contains(stderr, "unknown realtime subcommand") {
		t.Fatalf("disabled preview verb = %d %q, want a not-enabled explanation", code, stderr)
	}
	if occupancyCalls.Load() != 0 {
		t.Fatal("a disabled preview verb called its route")
	}

	occupancyEnabled.Store(true)
	_, _, stderr = runCLI(t, "realtime", "occupancy", "app", "endpoint", "lobby")
	if strings.Contains(stderr, "not enabled for this account") || occupancyCalls.Load() == 0 {
		t.Fatalf("served preview verb did not run: %q", stderr)
	}
}
