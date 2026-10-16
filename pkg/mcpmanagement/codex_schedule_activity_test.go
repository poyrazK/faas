package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// This runs the pinned real client through one complete recovery/watch workflow.
// Controls and synthetic aging exist only in this isolated test, never apid/MCP.
func TestCodexScheduleActivityAcceptance(t *testing.T) {
	binary := os.Getenv("GREGALE_MCP_CODEX_BINARY")
	if binary == "" {
		t.Skip("set GREGALE_MCP_CODEX_BINARY for real-client schedule acceptance")
	}
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts.AllowWrites = false
	seedSchedule(t, f, time.Now().Add(time.Hour))
	original := seedSchedule(t, f, time.Now().Add(2*time.Hour))
	create := func(ctx context.Context, at time.Time) (api.EnvironmentCacheSchedule, error) {
		id := uuid.NewString()
		return f.store.CreateEnvironmentCacheSchedule(ctx, f.account.ID, f.app.ID, "staging", api.CreateEnvironmentCacheScheduleRequest{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, ScheduleID: id, ExecuteAt: at, Purge: &api.CreateEnvironmentCachePurgeRequest{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, RequestID: id, Path: "/fixture-item"}})
	}
	var mu sync.Mutex
	armed := ""
	polls, arrivalPolls, revocationPolls, nativeWrites := 0, 0, 0, 0
	revoked := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		action := ""
		if r.Method != http.MethodGet {
			nativeWrites++
			mu.Unlock()
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == "/v1/apps/alpha/cache/schedules/activity" && armed != "" {
			polls++
			if armed == "arrival" {
				arrivalPolls = polls
			} else {
				revocationPolls = polls
			}
			if polls == 2 {
				action = armed
				armed = ""
			}
		}
		if action == "revoke" {
			revoked = true
		}
		denied := revoked
		mu.Unlock()
		if denied {
			api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Read access revoked", "Fixture permission revoked"))
			return
		}
		if action == "arrival" {
			if _, err := create(r.Context(), time.Now().Add(4*time.Hour)); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	opts.APIBase = backend.URL
	server, err := NewServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	access, err := randomToken("fixture_")
	if err != nil {
		t.Fatal(err)
	}
	handler := newWatchHTTPHandler(func(*http.Request) *mcp.Server { return server })
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer "+access {
			w.WriteHeader(403)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(endpoint.Close)
	controlToken, err := randomToken("fixture_")
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+controlToken {
			w.WriteHeader(403)
			return
		}
		var err error
		switch r.URL.Path {
		case "/during-recovery":
			var created, replacement api.EnvironmentCacheSchedule
			created, err = create(r.Context(), time.Now().Add(30*time.Minute))
			if err == nil {
				replacement, err = f.store.RescheduleEnvironmentCache(r.Context(), f.account.ID, f.app.ID, "staging", original.ScheduleID, api.RescheduleEnvironmentCacheRequest{ReplacementID: uuid.NewString(), AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, ExpectedExecuteAt: original.ExecuteAt, ExecuteAt: time.Now().Add(3 * time.Hour)})
			}
			if err == nil {
				writeOAuthJSON(w, 200, map[string]string{"created_id": created.ScheduleID, "original_id": original.ScheduleID, "replacement_id": replacement.ScheduleID})
				return
			}
		case "/arm-arrival", "/arm-revoke":
			mu.Lock()
			polls = 0
			if r.URL.Path == "/arm-arrival" {
				armed = "arrival"
			} else {
				armed = "revoke"
			}
			mu.Unlock()
		case "/offline-create":
			_, err = create(r.Context(), time.Now().Add(5*time.Hour))
		case "/expire-checkpoint":
			_, err = create(r.Context(), time.Now().Add(6*time.Hour))
			if err == nil {
				err = f.store.PruneEnvironmentCacheScheduleActivity(r.Context(), time.Now().Add(time.Minute))
			}
		case "/evidence":
			mu.Lock()
			defer mu.Unlock()
			writeOAuthJSON(w, 200, map[string]int{"native_writes": nativeWrites, "arrival_polls": arrivalPolls, "revocation_polls": revocationPolls})
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
	script, err := filepath.Abs("../../scripts/ops/management-mcp-schedule-acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", script, "--codex-binary", binary)
	command.Env = append(os.Environ(), "GREGALE_MCP_TEST_RESOURCE="+endpoint.URL+"/mcp", "GREGALE_MCP_ACCESS_TOKEN="+access, "GREGALE_MCP_TEST_CONTROL="+control.URL, "GREGALE_MCP_TEST_CONTROL_TOKEN="+controlToken)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex schedule activity acceptance failed: %v\n%s", err, output)
	}
	var report map[string]any
	if json.Unmarshal(output, &report) != nil {
		t.Fatal("invalid real-client schedule report")
	}
	for _, key := range []string{"recovery", "concurrent_changes", "watch_arrival", "timeout_checkpoint", "reconnect", "cursor_expiry", "recovery_restart", "access_revocation"} {
		if report[key] != "passed" {
			t.Fatalf("missing schedule evidence: %s", key)
		}
	}
	if report["client"] != "codex" || report["fixture"] != true || report["model_requests"] != float64(0) || report["mcp_writes"] != float64(0) {
		t.Fatal("invalid schedule acceptance provenance")
	}
	mu.Lock()
	writes := nativeWrites
	mu.Unlock()
	f.mu.Lock()
	fixtureWrites := f.writes
	f.mu.Unlock()
	if writes != 0 || fixtureWrites != 0 {
		t.Fatal("schedule acceptance issued a native mutation")
	}
	if path := os.Getenv("GREGALE_MCP_SCHEDULE_REPORT"); path != "" {
		if err := os.WriteFile(path, output, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
