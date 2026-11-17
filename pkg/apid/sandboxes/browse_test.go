// adr: 20261116182615 §3–§4 — a gregale_browse task is admitted like a
// browser session on web egress, under the same switches and plan limits,
// once per call, exposing no port; its result is read back in full.
package sandboxes_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func (f *fakeStore) CreateSandboxTask(ctx context.Context, a agentstore.SandboxAdmission, call agentstore.SandboxTaskCall) (agentstore.Sandbox, bool, error) {
	f.mu.Lock()
	if id, ok := f.taskCalls[call]; ok {
		defer f.mu.Unlock()
		for _, sb := range f.sessions {
			if sb.ID == id {
				return sb, true, nil
			}
		}
	}
	f.mu.Unlock()
	sb, err := f.CreateSandbox(ctx, a)
	if err != nil {
		return sb, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taskCalls == nil {
		f.taskCalls = map[agentstore.SandboxTaskCall]string{}
	}
	f.taskCalls[call] = sb.ID
	return sb, false, nil
}

func (f *fakeStore) SandboxTaskResult(_ context.Context, _, id string) (agentstore.SandboxTaskResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res, ok := f.taskResults[id]
	if !ok {
		return res, agentstore.ErrNotFound
	}
	return res, nil
}

func TestAdmitBrowse(t *testing.T) {
	f := newFixture(t)
	f.h.PreviewDomain = previewDomain
	call := agentstore.SandboxTaskCall{EndpointID: "ep-1", ThreadID: "t-1", ToolCallID: "call_1", IssuedAt: f.now}
	ctx := context.Background()
	if _, _, p := f.h.AdmitBrowse(ctx, pro(), "https://example.com/", 0, call); p == nil || p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("with the switches off = %+v", p)
	}
	f.h.Browser, f.h.WebNetwork = true, true
	for name, tc := range map[string]struct {
		acct     state.Account
		url      string
		maxChars int
	}{
		"a private host":    {pro(), "http://10.0.0.1/", 0},
		"credentials":       {pro(), "https://u:p@example.com/", 0},
		"another scheme":    {pro(), "file:///etc/passwd", 0},
		"too many chars":    {pro(), "https://example.com/", api.SandboxBrowseMaxChars + 1},
		"a plan without it": {state.Account{ID: account, Plan: api.PlanFree}, "https://example.com/", 0},
	} {
		if _, _, p := f.h.AdmitBrowse(ctx, tc.acct, tc.url, tc.maxChars, call); p == nil || p.Status >= 500 {
			t.Errorf("%s: admitted, or %+v", name, p)
		}
	}
	if len(f.store.sessions) != 0 {
		t.Fatalf("a refused task created %d sessions", len(f.store.sessions))
	}
	sb, existing, p := f.h.AdmitBrowse(ctx, pro(), "https://example.com/", 0, call)
	a := f.store.admitted
	if p != nil || existing || !a.Browser || a.NetworkMode != api.SandboxNetworkWeb || len(a.Ports) != 0 || a.RAMMB < api.SandboxBrowserRAMMBMin ||
		a.Task == nil || *a.Task != (agentstore.SandboxTask{Kind: agentstore.SandboxTaskBrowse, URL: "https://example.com/", MaxChars: api.SandboxBrowseDefaultChars}) {
		t.Fatalf("admitted %+v, %v, %+v", a, existing, p)
	}
	if want := f.now.Add(time.Duration(api.SandboxTaskTimeoutSec[api.PlanPro.PlanIndex()])*time.Second + 3*time.Minute); !a.ExpiresAt.Equal(want) {
		t.Fatalf("expires at %v, want %v", a.ExpiresAt, want)
	}
	again, existing, p := f.h.AdmitBrowse(ctx, pro(), "https://example.com/", 0, call)
	if p != nil || !existing || again.ID != sb.ID || len(f.store.sessions) != 1 {
		t.Fatalf("a retried call: %+v, %v, %+v, %d sessions", again, existing, p, len(f.store.sessions))
	}
}

func TestSandboxTaskResult(t *testing.T) {
	f := newFixture(t)
	zero, one := 0, 1
	f.store.taskResults = map[string]agentstore.SandboxTaskResult{
		"ok":     {State: "closed", Kind: "browse", URL: "https://e.com/", ExitCode: &zero, Output: []byte(`{"title":"E"}`)},
		"failed": {State: "failed", Kind: "browse", URL: "https://e.com/", ExitCode: &one, Output: []byte("net::ERR\n"), OutcomeCode: "task_failed"},
	}
	var res api.SandboxTaskResult
	if code := f.call(t, f.h.Task, pro(), http.MethodGet, "", "", "ok", &res); code != http.StatusOK || string(res.Result) != `{"title":"E"}` ||
		res.Output != "" || res.SandboxID != "ok" {
		t.Fatalf("a fetched page = %d %+v", code, res)
	}
	res = api.SandboxTaskResult{}
	if code := f.call(t, f.h.Task, pro(), http.MethodGet, "", "", "failed", &res); code != http.StatusOK || res.Result != nil ||
		res.Output != "net::ERR\n" || res.Outcome == nil || res.Outcome.Code != "task_failed" {
		t.Fatalf("a failed fetch = %d %+v", code, res)
	}
	if code := f.call(t, f.h.Task, pro(), http.MethodGet, "", "", "nope", nil); code != http.StatusNotFound {
		t.Fatalf("an unknown task = %d", code)
	}
}
