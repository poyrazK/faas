package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const sessionID = "0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10"

// fakeSessionAPI answers the session routes for one persistent session.
type fakeSessionAPI struct {
	mu        sync.Mutex
	state     string
	calls     []string
	listQuery string
	fork      api.ForkSandboxRequest
}

func (f *fakeSessionAPI) view() api.Sandbox {
	s := api.Sandbox{ID: sessionID, State: f.state, Kind: "persistent", Preset: "codex", RAMMB: 2048, VolumeGB: 10, Token: "sbx-secret"}
	if f.state == "paused" {
		at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
		s.PausedAt, s.Durability, s.StoredBytes = &at, "remote", 300<<20
	}
	return s
}

func (f *fakeSessionAPI) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		base := "/v1/sandboxes/" + sessionID
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes":
			f.listQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(api.SandboxList{Items: []api.Sandbox{f.view()}})
		case r.Method == http.MethodGet && r.URL.Path == base:
			// A pause or resume settles on the first read after it.
			switch f.state {
			case "pausing":
				f.state = "paused"
			case "resuming":
				f.state = "ready"
			}
			_ = json.NewEncoder(w).Encode(f.view())
		case r.Method == http.MethodPost && r.URL.Path == base+"/pause":
			f.state = "pausing"
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(f.view())
		case r.Method == http.MethodPost && r.URL.Path == base+"/resume":
			f.state = "resuming"
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(f.view())
		case r.Method == http.MethodPost && r.URL.Path == base+"/fork":
			_ = json.NewDecoder(r.Body).Decode(&f.fork)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.ForkSandboxResponse{Forks: []api.Sandbox{{ID: "f0f0f0f0-0000-4000-8000-000000000001", State: "queued", Kind: "ephemeral", Token: "sbx-fork"}}})
		case r.Method == http.MethodDelete && r.URL.Path == base:
			f.state = "closed"
			_ = json.NewEncoder(w).Encode(f.view())
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runSessionCLI(t *testing.T, srv *httptest.Server, args ...string) (int, string) {
	t.Helper()
	main.ResetJSONOut(t)
	old := *main.SandboxSessionPoll
	*main.SandboxSessionPoll = time.Millisecond
	t.Cleanup(func() { *main.SandboxSessionPoll = old })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run(args)
	restore()
	return code, out.String()
}

// `gregale sandbox session` pauses and waits, lists paused sessions,
// resumes and waits, forks and closes; it never prints a session token.
func TestSandboxSessionPauseListResumeForkRm(t *testing.T) {
	f := &fakeSessionAPI{state: "ready"}
	srv := f.serve(t)

	code, out := runSessionCLI(t, srv, "sandbox", "session", "pause", sessionID)
	if code != 0 || !strings.Contains(out, "is paused") {
		t.Fatalf("pause = %d %q, want 0 and paused", code, out)
	}
	code, out = runSessionCLI(t, srv, "sandbox", "session", "list", "--paused")
	if code != 0 || f.listQuery != "state=paused" || !strings.Contains(out, sessionID) || !strings.Contains(out, "300.0 MiB") {
		t.Fatalf("list --paused = %d query %q out %q", code, f.listQuery, out)
	}
	code, out = runSessionCLI(t, srv, "sandbox", "session", "get", sessionID)
	if code != 0 || !strings.Contains(out, "durability:  remote") {
		t.Fatalf("get = %d %q", code, out)
	}
	code, out = runSessionCLI(t, srv, "--json", "sandbox", "session", "get", sessionID)
	if code != 0 || strings.Contains(out, "sbx-secret") || !strings.Contains(out, `"stored_bytes"`) {
		t.Fatalf("get --json = %d %q, want no token and stored_bytes", code, out)
	}
	code, out = runSessionCLI(t, srv, "sandbox", "session", "resume", sessionID)
	if code != 0 || !strings.Contains(out, "is ready") {
		t.Fatalf("resume = %d %q, want 0 and ready", code, out)
	}
	code, out = runSessionCLI(t, srv, "sandbox", "session", "fork", sessionID, "--count", "1")
	if code != 0 || f.fork.Count != 1 || !strings.Contains(out, "f0f0f0f0-0000-4000-8000-000000000001") || strings.Contains(out, "sbx-fork") {
		t.Fatalf("fork = %d %q (request %+v)", code, out, f.fork)
	}
	if code, out = runSessionCLI(t, srv, "sandbox", "session", "delete", sessionID); code != 0 || f.state != "closed" {
		t.Fatalf("delete = %d %q, state %s", code, out, f.state)
	}
	if code, _ = runSessionCLI(t, srv, "sandbox", "session", "pause"); code != 1 {
		t.Errorf("pause without an id = %d, want 1", code)
	}
}

// `--no-wait` asks once and does not poll.
func TestSandboxSessionPauseNoWait(t *testing.T) {
	f := &fakeSessionAPI{state: "ready"}
	srv := f.serve(t)
	code, out := runSessionCLI(t, srv, "sandbox", "session", "pause", sessionID, "--no-wait")
	if code != 0 || !strings.Contains(out, "is pausing") {
		t.Fatalf("pause --no-wait = %d %q", code, out)
	}
	if len(f.calls) != 1 {
		t.Errorf("pause --no-wait made %v, want one call", f.calls)
	}
}

// --json prints the session list as NDJSON without session tokens.
func TestSandboxSessionListJSON(t *testing.T) {
	f := &fakeSessionAPI{state: "paused"}
	srv := f.serve(t)
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"--json", "sandbox-sessions", "list", "--paused"})
	restore()
	var s api.Sandbox
	if code != 0 || json.Unmarshal([]byte(strings.TrimSpace(out.String())), &s) != nil || s.ID != sessionID || s.Token != "" || s.StoredBytes == 0 {
		t.Fatalf("list --json = %d %q", code, out.String())
	}
}
