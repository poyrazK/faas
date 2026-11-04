package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func writeHostedTestApp(t *testing.T, dir string) {
	t.Helper()
	main.WriteSuiteTestFile(t, filepath.Join(dir, "package.json"), `{"name":"rehearsal","scripts":{"start":"node server.js"}}`)
	main.WriteSuiteTestFile(t, filepath.Join(dir, "server.js"), "require('http').createServer((r,s)=>s.end('ok')).listen(8080);\n")
}

// A capability the platform has not enabled refuses every slot the same way,
// so the first refusal skips the rest instead of creating a session for each.
func TestRehearsalStopsAfterTheFirstNotEnabledRefusal(t *testing.T) {
	main.CaptureSuiteTestOutput(t)
	o, _ := main.RehearsalTestRepo(t)
	writeHostedTestApp(t, filepath.Join(o.Repo, "checks"))
	main.RehearsalTestGit(t, o.Repo, "add", "checks/package.json", "checks/server.js")
	main.RehearsalTestGit(t, o.Repo, "commit", "-qm", "hosted app source")
	o.Base = main.RehearsalTestGit(t, o.Repo, "rev-parse", "HEAD")
	o.Candidate, o.Repeat, o.Engine, o.MaxWorkloadMinutes = o.Base, 3, "real-vm", 30
	session, err := main.PrepareTestRehearsal(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(*session.Root()) })
	var mu sync.Mutex
	upserts, destroys := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "PUT" && r.URL.Path == "/v1/dev/sessions/rehearsal-app":
			upserts++
			_ = json.NewEncoder(w).Encode(api.DevSessionResponse{App: api.AppResponse{Slug: "rehearsal-app-dev"}})
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/dev/test-runs/"):
			api.WriteProblem(w, api.ErrRehearsalNotEnabled("Rehearsal network containment"))
		case r.Method == "DELETE" && r.URL.Path == "/v1/dev/sessions/rehearsal-app":
			destroys++
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	report := main.RunTestRehearsal(context.Background(), main.NewClient(server.URL, "synthetic-key"), session, o)
	runs := append(append([]main.TestRunReceipt{}, report.BaselineRuns...), report.CandidateRuns...)
	if report.Status != "inconclusive" || upserts != 1 || destroys != 1 || len(runs) != 6 {
		t.Fatalf("status=%s upserts=%d destroys=%d runs=%d, want one refused slot and the rest skipped", report.Status, upserts, destroys, len(runs))
	}
	if !strings.Contains(runs[0].Error, api.CodeRehearsalNotEnabled) {
		t.Fatalf("first slot error = %q, want the refusal", runs[0].Error)
	}
	for _, receipt := range runs[1:] {
		if receipt.Status != "skipped" || !strings.Contains(receipt.SkipReason, "has not enabled") {
			t.Fatalf("later slot ran after the refusal: %+v", receipt)
		}
	}
}

// CI sends SIGKILL seconds after SIGTERM, so an interrupted run destroys its
// sessions before anything slow, and skips diagnostics of destroyed sessions.
func TestRealVMRunDestroysSessionsFirstWhenInterrupted(t *testing.T) {
	dir := t.TempDir()
	writeHostedTestApp(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method == "PUT" && r.URL.Path == "/v1/dev/sessions/interrupt-api":
			_ = json.NewEncoder(w).Encode(api.DevSessionResponse{App: api.AppResponse{Slug: "interrupt-app"}})
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/dev/test-runs/"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "PATCH" && r.URL.Path == "/v1/apps/interrupt-app":
			cancel() // The signal arrives while the run deploys.
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == "DELETE":
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	receipt := main.RunTestProfile(ctx, main.NewClient(server.URL, "synthetic-key"), "interrupt", main.TestScenario{Project: "interrupt-api", Source: "."}, dir, "warm")
	if receipt.Status != "failed" || !strings.Contains(receipt.CleanupError, "run interrupted") {
		t.Fatalf("receipt status=%s cleanup=%q, want a failed run that reports the interruption", receipt.Status, receipt.CleanupError)
	}
	mu.Lock()
	defer mu.Unlock()
	patch := -1
	for i, call := range calls {
		if call == "PATCH /v1/apps/interrupt-app" {
			patch = i
		}
	}
	after := calls[patch+1:]
	if patch < 0 || len(after) != 2 || after[0] != "DELETE /v1/dev/sessions/interrupt-api" || !strings.HasPrefix(after[1], "DELETE /v1/dev/test-runs/") {
		t.Fatalf("requests after the interrupt = %v, want the session destroyed first and nothing else but the namespace release", after)
	}
}

// A create whose response never arrived may have committed; the runner
// destroys that session instead of leaving it to its lease.
func TestRealVMRunReleasesASessionWhoseCreateResponseWasLost(t *testing.T) {
	dir := t.TempDir()
	writeHostedTestApp(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var destroyed []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PUT" && r.URL.Path == "/v1/dev/sessions/lost-api":
			// The server commits; the caller is interrupted before the reply.
			// Reading the body lets the server notice the disconnect.
			_, _ = io.Copy(io.Discard, r.Body)
			cancel()
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case r.Method == "DELETE" && r.URL.Path == "/v1/dev/sessions/lost-api":
			mu.Lock()
			destroyed = append(destroyed, r.URL.Query().Get("workspace_id"))
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	receipt := main.RunTestProfile(ctx, main.NewClient(server.URL, "synthetic-key"), "lost", main.TestScenario{Project: "lost-api", Source: "."}, dir, "warm")
	mu.Lock()
	defer mu.Unlock()
	if receipt.Error == "" || len(destroyed) != 1 || destroyed[0] != receipt.RunID {
		t.Fatalf("error=%q destroyed=%v run=%s, want the unconfirmed session destroyed by its run ID", receipt.Error, destroyed, receipt.RunID)
	}
}

func TestRehearsalLocalExecutionNotice(t *testing.T) {
	trigger := []string{"sh", "trigger.sh"}
	for _, tc := range []struct {
		name     string
		scenario main.TestScenario
		engine   string
		local    bool
	}{
		{"local engine", main.TestScenario{}, "local", true},
		{"simulated engine", main.TestScenario{}, "simulated", true},
		{"hosted recipe command", main.TestScenario{Trigger: trigger}, "real-vm", true},
		{"hosted step command", main.TestScenario{Steps: []main.TestScenarioStep{{Name: "seed", Command: trigger}}}, "real-vm", true},
		{"contained recipe command", main.TestScenario{Trigger: trigger, CommandExecution: &main.TestCommandExecutionSpec{}}, "real-vm", false},
		{"hosted without commands", main.TestScenario{}, "real-vm", false},
	} {
		if got := main.TestRehearsalLocalExecution(tc.scenario, tc.engine); (got != "") != tc.local {
			t.Fatalf("%s: notice %q, want local=%v", tc.name, got, tc.local)
		}
	}
}
