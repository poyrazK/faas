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

// fakeRunApprovalsAPI answers the run approval routes (ADR-20261027090016
// amendment 2) and keeps what the CLI sent.
type fakeRunApprovalsAPI struct {
	mu     sync.Mutex
	method string
	path   string
	body   api.DecideAgentRunApprovalRequest
	closed bool
}

func (f *fakeRunApprovalsAPI) serve(t *testing.T) *httptest.Server {
	t.Helper()
	expires := time.Date(2026, 11, 19, 12, 5, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.method, f.path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/agent-runs/run-1/approvals":
			_ = json.NewEncoder(w).Encode(api.AgentRunApprovalList{Items: []api.AgentRunApproval{{
				ID: "apr-1", RunID: "run-1", Tool: "github.merge_pull_request", State: api.AgentRunApprovalPending,
				ArgumentsSummary: "merge #42 into main\x1b]52;c;evil\x07 " + strings.Repeat("x", 100), ExpiresAt: expires,
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agent-runs/run-1/approvals/apr-1":
			_ = json.NewDecoder(r.Body).Decode(&f.body)
			if f.closed {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: http.StatusConflict, Code: api.CodeAgentRunApprovalClosed,
					Title: "Approval closed", Detail: "the approval is expired; a decision is accepted only while it is pending"})
				return
			}
			state := api.AgentRunApprovalApproved
			if f.body.Decision == api.AgentRunApprovalDecisionDeny {
				state = api.AgentRunApprovalDenied
			}
			_ = json.NewEncoder(w).Encode(api.AgentRunApproval{ID: "apr-1", RunID: "run-1", Tool: "github.merge_pull_request",
				State: state, Note: f.body.Note, ExpiresAt: expires})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAgentsRunsApprovals(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		closed     bool
		wantCode   int
		wantMethod string
		wantPath   string
		wantBody   api.DecideAgentRunApprovalRequest
		wantOut    []string
		notOut     []string
		wantErr    []string
	}{
		{
			name: "list", args: []string{"agents", "runs", "approvals", "run-1"},
			wantMethod: http.MethodGet, wantPath: "/v1/agent-runs/run-1/approvals",
			wantOut: []string{"ID", "TOOL", "STATE", "EXPIRES", "ARGUMENTS", "apr-1", "github.merge_pull_request", "pending", "2026-11-19T12:05:00Z", "merge #42 into main", "…"},
			notOut:  []string{"\x1b", "\x07", strings.Repeat("x", 100)},
		},
		{
			name: "list in the tree's names", args: []string{"agent", "run", "approval", "list", "run-1"},
			wantMethod: http.MethodGet, wantPath: "/v1/agent-runs/run-1/approvals",
			wantOut: []string{"apr-1", "pending"},
		},
		{
			name: "deny in the tree's names", args: []string{"agent", "run", "deny", "run-1", "apr-1"},
			wantMethod: http.MethodPost, wantPath: "/v1/agent-runs/run-1/approvals/apr-1",
			wantBody: api.DecideAgentRunApprovalRequest{Decision: "deny"},
			wantOut:  []string{"apr-1", "denied"},
		},
		{
			name: "list json", args: []string{"agents", "runs", "approvals", "run-1", "--json"},
			wantMethod: http.MethodGet, wantPath: "/v1/agent-runs/run-1/approvals",
			wantOut: []string{`"items"`, `"id": "apr-1"`, strings.Repeat("x", 100)},
		},
		{
			name: "approve", args: []string{"agents", "runs", "approve", "run-1", "apr-1", "--note", "looks right"},
			wantMethod: http.MethodPost, wantPath: "/v1/agent-runs/run-1/approvals/apr-1",
			wantBody: api.DecideAgentRunApprovalRequest{Decision: "approve", Note: "looks right"},
			wantOut:  []string{"apr-1", "approved"},
		},
		{
			name: "deny json", args: []string{"agents", "runs", "deny", "--note", "no", "run-1", "apr-1", "--json"},
			wantMethod: http.MethodPost, wantPath: "/v1/agent-runs/run-1/approvals/apr-1",
			wantBody: api.DecideAgentRunApprovalRequest{Decision: "deny", Note: "no"},
			wantOut:  []string{`"state": "denied"`, `"note": "no"`},
		},
		{
			name: "approve closed", args: []string{"agents", "runs", "approve", "run-1", "apr-1"}, closed: true,
			wantCode: 1, wantMethod: http.MethodPost, wantPath: "/v1/agent-runs/run-1/approvals/apr-1",
			wantBody: api.DecideAgentRunApprovalRequest{Decision: "approve"},
			wantErr:  []string{"Approval closed", "the approval is expired", "gregale agent run approval list run-1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunApprovalsAPI{closed: tc.closed}
			srv := f.serve(t)
			main.ResetJSONOut(t)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			stderr, restoreErr := main.CaptureStderr(t)
			stdout, restoreOut := main.CaptureStdout(t)
			code := main.Run(tc.args)
			restoreOut()
			restoreErr()
			out, errOut := stdout.String(), stderr.String()
			if (code == 0) != (tc.wantCode == 0) {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, out, errOut)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.method != tc.wantMethod || f.path != tc.wantPath || f.body != tc.wantBody {
				t.Errorf("request %s %s %+v, want %s %s %+v", f.method, f.path, f.body, tc.wantMethod, tc.wantPath, tc.wantBody)
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
			}
			for _, not := range tc.notOut {
				if strings.Contains(out, not) {
					t.Errorf("stdout has %q:\n%q", not, out)
				}
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr lacks %q:\n%s", want, errOut)
				}
			}
		})
	}
}

// A decision needs both the run and the approval; nothing is sent without them.
func TestAgentsRunsDecideNeedsBothIDs(t *testing.T) {
	f := &fakeRunApprovalsAPI{}
	srv := f.serve(t)
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	stderr, restore := main.CaptureStderr(t)
	code := main.Run([]string{"agents", "runs", "deny", "run-1"})
	restore()
	if code == 0 || f.method != "" || !strings.Contains(stderr.String(), "usage: gregale agent run deny <run-id> <approval-id>") {
		t.Fatalf("exit %d, request %s %s, stderr:\n%s", code, f.method, f.path, stderr.String())
	}
}
