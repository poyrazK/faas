package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Real client, isolated loopback fixtures, bridge-local mutations only. A
// drained bridge restart reopens the actual encrypted journal using its key.
func TestCodexActivityConsumerAcceptance(t *testing.T) {
	binary := os.Getenv("GREGALE_MCP_CODEX_BINARY")
	if binary == "" {
		t.Skip("set GREGALE_MCP_CODEX_BINARY for real-client consumer acceptance")
	}
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, key := consumerOptions(t, opts)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	var nativeWrites atomic.Int64
	var denied atomic.Bool
	var appendPoll atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			nativeWrites.Add(1)
			w.WriteHeader(405)
			return
		}
		if denied.Load() {
			api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Read access revoked", "Fixture permission revoked"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/activity") && appendPoll.Load() > 0 && appendPoll.Add(-1) == 0 {
			seedScheduleContext(r.Context(), t, f, time.Now().Add(3*time.Hour))
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	opts.APIBase = backend.URL
	opts.Lifecycle = NewLifecycle()
	current, err := NewServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.RWMutex
	t.Cleanup(func() { mu.Lock(); defer mu.Unlock(); _ = store.Close() })
	access, err := randomToken("fixture_")
	if err != nil {
		t.Fatal(err)
	}
	handler := newWatchHTTPHandler(func(*http.Request) *mcp.Server { mu.RLock(); defer mu.RUnlock(); return current })
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer "+access {
			w.WriteHeader(403)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(endpoint.Close)
	token, err := randomToken("fixture_")
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(403)
			return
		}
		var err error
		switch r.URL.Path {
		case "/append-on-poll":
			appendPoll.Store(2)
		case "/append", "/expire":
			seedScheduleContext(r.Context(), t, f, time.Now().Add(2*time.Hour))
			if r.URL.Path == "/expire" {
				err = f.store.PruneEnvironmentCacheScheduleActivity(r.Context(), time.Now().Add(time.Minute))
			}
		case "/restart":
			mu.Lock()
			defer mu.Unlock()
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			err = opts.Lifecycle.Shutdown(ctx)
			if err == nil {
				err = store.Close()
			}
			if err == nil {
				store, err = OpenStateStore(path, key)
			}
			if err == nil {
				opts.planStore = store
				opts.Lifecycle = NewLifecycle()
				current, err = NewServer(api.NewClient(backend.URL, testToken), opts)
			}
		case "/deny":
			denied.Store(true)
		case "/evidence":
			writeOAuthJSON(w, 200, map[string]int64{"native_writes": nativeWrites.Load()})
			return
		default:
			w.WriteHeader(404)
			return
		}
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		writeOAuthJSON(w, 200, map[string]bool{"ok": true})
	}))
	t.Cleanup(control.Close)
	script, err := filepath.Abs("../../scripts/ops/management-mcp-consumer-acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", script, "--codex-binary", binary)
	command.Env = append(os.Environ(), "GREGALE_MCP_TEST_RESOURCE="+endpoint.URL+"/mcp", "GREGALE_MCP_ACCESS_TOKEN="+access, "GREGALE_MCP_TEST_CONTROL="+control.URL, "GREGALE_MCP_TEST_CONTROL_TOKEN="+token)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex consumer acceptance failed: %v\n%s", err, output)
	}
	var report map[string]any
	if json.Unmarshal(output, &report) != nil {
		t.Fatal("invalid consumer acceptance report")
	}
	for _, field := range []string{"listing", "acknowledgment", "retirement", "stale_requests", "restart", "recreation", "expiry_retirement", "lag_diagnostics", "consumer_watch", "consumer_recovery", "consumer_resources", "consumer_health", "access_revocation"} {
		if report[field] != "passed" {
			t.Fatalf("missing consumer acceptance evidence: %s", field)
		}
	}
	calls, ok := report["bridge_mutation_calls"].(float64)
	if report["client"] != "codex" || report["fixture"] != true || report["model_requests"] != float64(0) || report["native_writes"] != float64(0) || !ok || calls < 6 {
		t.Fatal("invalid consumer evidence provenance")
	}
	if nativeWrites.Load() != 0 || f.writes != 0 {
		t.Fatal("consumer workflow wrote native intent")
	}
	mu.RLock()
	var records map[string]activityConsumerRecord
	err = store.read("activity-consumers/"+opts.planNamespace, &records)
	mu.RUnlock()
	if err != nil || len(records) != 3 {
		t.Fatal("denied operations changed consumers", err, len(records))
	}
	if reportPath := os.Getenv("GREGALE_MCP_CONSUMER_REPORT"); reportPath != "" {
		if err = os.WriteFile(reportPath, output, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
