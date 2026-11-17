// adr: 20261117065818 §3 — a gregale_browser session is admitted like a
// browser session on web egress, under the same switches and plan limits,
// with the plan's browser idle close and lifetime, once per call, exposing
// no port; an action is read back in full.
package sandboxes_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/sandboxes"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
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

func (f *fakeStore) SandboxActionResult(_ context.Context, _, id string, seq int) (agentstore.SandboxActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res, ok := f.actionResults[fmt.Sprintf("%s/%d", id, seq)]
	if !ok {
		return res, agentstore.ErrNotFound
	}
	return res, nil
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

// adr: 20261117065818 §5 — a kept screenshot is served as a JPEG to the
// account; an action without one, or past its retention, is 404.
func TestActionScreenshot(t *testing.T) {
	f := newFixture(t)
	files, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.h.Files = files
	image := []byte("\xff\xd8\xff\xe0 jpeg")
	key := "sandbox-screenshots/a/s/x.jpg"
	if err := files.Put(context.Background(), key, bytes.NewReader(image)); err != nil {
		t.Fatal(err)
	}
	f.store.actionResults = map[string]agentstore.SandboxActionResult{
		"sbx/2": {Kind: "snapshot", State: "done", Screenshot: agentstore.SandboxScreenshot{Key: key, Bytes: int64(len(image))}},
		"sbx/3": {Kind: "click", State: "done"},
	}
	get := func(seq string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/sbx/actions/"+seq+"/screenshot", nil)
		r.SetPathValue("id", "sbx")
		r.SetPathValue("seq", seq)
		w := httptest.NewRecorder()
		f.h.ActionScreenshot(w, r, pro())
		return w
	}
	if w := get("2"); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), image) || w.Header().Get("Content-Type") != "image/jpeg" ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("a kept screenshot = %d %q %v", w.Code, w.Body.Bytes(), w.Header())
	}
	for _, seq := range []string{"3", "4", "0", "x"} {
		if w := get(seq); w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Errorf("seq %s = %d", seq, w.Code)
		}
	}
}
