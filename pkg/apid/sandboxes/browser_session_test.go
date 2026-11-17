// adr: 20261117065818 §3 — a gregale_browser session is admitted like a
// browser session on web egress, under the same switches and plan limits,
// with the plan's browser idle close and lifetime, once per call, exposing
// no port; an action is read back in full.
package sandboxes_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/sandboxes"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func (f *fakeStore) CreateBrowserSession(ctx context.Context, a agentstore.SandboxAdmission, args agentstore.SandboxActionArgs,
	call agentstore.SandboxTaskCall) (agentstore.Sandbox, bool, error) {
	f.mu.Lock()
	f.openArgs = args
	f.mu.Unlock()
	return f.CreateSandboxTask(ctx, a, call)
}

func (f *fakeStore) BrowserSessionThread(context.Context, string, string) (agentstore.SandboxThread, error) {
	return agentstore.SandboxThread{}, agentstore.ErrNotFound
}

func (f *fakeStore) CreateSandboxAction(context.Context, string, string, string, agentstore.SandboxActionArgs,
	agentstore.SandboxTaskCall) (agentstore.SandboxAction, bool, error) {
	return agentstore.SandboxAction{}, false, nil
}

func (f *fakeStore) SandboxActionResult(context.Context, string, string, int) (agentstore.SandboxActionResult, error) {
	return agentstore.SandboxActionResult{}, agentstore.ErrNotFound
}

func TestAdmitBrowserSession(t *testing.T) {
	f := newFixture(t)
	f.h.PreviewDomain = previewDomain
	call := agentstore.SandboxTaskCall{EndpointID: "ep-1", ThreadID: "t-1", ToolCallID: "call_1", IssuedAt: f.now}
	ctx := context.Background()
	if _, _, p := f.h.AdmitBrowserSession(ctx, pro(), "https://example.com/", 4000, call); p == nil || p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("with the switches off = %+v", p)
	}
	f.h.Browser, f.h.WebNetwork = true, true
	for name, acct := range map[string]state.Account{"a plan without it": {ID: account, Plan: api.PlanFree}} {
		if _, _, p := f.h.AdmitBrowserSession(ctx, acct, "https://example.com/", 4000, call); p == nil || p.Status >= 500 {
			t.Errorf("%s: admitted, or %+v", name, p)
		}
	}
	if _, _, p := f.h.AdmitBrowserSession(ctx, pro(), "http://10.0.0.1/", 4000, call); p == nil || p.Status >= 500 {
		t.Fatalf("a private host: %+v", p)
	}
	if len(f.store.sessions) != 0 {
		t.Fatalf("a refused open created %d sessions", len(f.store.sessions))
	}
	sb, existing, p := f.h.AdmitBrowserSession(ctx, pro(), "https://example.com/", 4000, call)
	a := f.store.admitted
	idx := api.PlanPro.PlanIndex()
	if p != nil || existing || !a.Browser || a.NetworkMode != api.SandboxNetworkWeb || len(a.Ports) != 0 || a.Task != nil ||
		a.IdleTimeoutSec != api.SandboxBrowserIdleCloseSec[idx] || f.store.openArgs != (agentstore.SandboxActionArgs{URL: "https://example.com/", MaxChars: 4000}) {
		t.Fatalf("admitted %+v, %v, %+v, args %+v", a, existing, p, f.store.openArgs)
	}
	if want := f.now.Add(time.Duration(api.SandboxBrowserLifetimeSec[idx]) * time.Second); !a.ExpiresAt.Equal(want) {
		t.Fatalf("expires at %v, want %v", a.ExpiresAt, want)
	}
	again, existing, p := f.h.AdmitBrowserSession(ctx, pro(), "https://example.com/", 4000, call)
	if p != nil || !existing || again.ID != sb.ID || len(f.store.sessions) != 1 {
		t.Fatalf("a retried open: %+v, %v, %+v, %d sessions", again, existing, p, len(f.store.sessions))
	}
}

func TestActionResultView(t *testing.T) {
	zero, one := 0, 1
	ok := sandboxes.ActionResultView("sbx", 2, agentstore.SandboxActionResult{Kind: "click", Args: agentstore.SandboxActionArgs{Ref: 4},
		State: "done", ExitCode: &zero, Output: []byte(`{"title":"E"}`)})
	if ok.SandboxID != "sbx" || ok.Seq != 2 || ok.Ref != 4 || string(ok.Result) != `{"title":"E"}` || ok.Output != "" {
		t.Fatalf("a snapshot = %+v", ok)
	}
	failed := sandboxes.ActionResultView("sbx", 3, agentstore.SandboxActionResult{Kind: "click", State: "done", ExitCode: &one,
		Output: []byte("ref 9 is not on the page\n")})
	if failed.Result != nil || failed.Output != "ref 9 is not on the page\n" {
		t.Fatalf("a failed click = %+v", failed)
	}
	pending := sandboxes.ActionResultView("sbx", 4, agentstore.SandboxActionResult{Kind: "back", State: "pending"})
	if pending.State != "pending" || pending.Result != nil || pending.Output != "" || pending.ExitCode != nil {
		t.Fatalf("a pending action = %+v", pending)
	}
}
