// adr: 20261111150000
//
// A typed client's send() over the real node-local service proxy
// (ADR-20261111150000 slice 4): gatewayd-internal's chained async admitter
// enqueues the call as a durable invocation of the target in Postgres and
// answers 202 with its id, without forwarding it to a guest; a call without
// the marker is forwarded as before.
//
// Build tag: (none). Requires Postgres (skip via FAAS_SKIP_PG_TESTS). No KVM.
package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestE2E_ServiceMesh_FunctionSendEnqueuesInvocation(t *testing.T) {
	f := newNormalPathFixture(t, "send-caller")
	if f == nil {
		return
	}
	target := createServiceApp(t, f, "sendusers", nil)
	f.vmmd.SetDefaultVersion("v1")
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID:       target.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("5", 64),
	})
	if err != nil {
		t.Fatalf("create target deployment: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark target deployment live: %v", err)
	}
	publishNormalPathLayer(t, f, dep.ID)

	send := http.Header{}
	send.Set(api.FunctionInvokeHeader, api.FunctionInvokeAsync)
	send.Set(api.FunctionCallHeader, "1")
	send.Set("Idempotency-Key", "send-e2e-1")
	var status int
	var header http.Header
	var body string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status, header, body = serviceCallWithMethod(t, f.h, f.app.ID, "sendusers", http.MethodPost, "/users", send)
		if status == http.StatusAccepted {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if status != http.StatusAccepted {
		t.Fatalf("send status = %d, want 202; body=%s", status, body)
	}
	var accepted api.AsyncInvokeResponse
	if err := json.Unmarshal([]byte(body), &accepted); err != nil || accepted.ID == "" || header.Get(api.InvocationIDHeader) != accepted.ID {
		t.Fatalf("202 body = %s (%v), want an invocation id echoed in %s", body, err, api.InvocationIDHeader)
	}
	inv, err := f.store.InvocationByID(f.ctx, accepted.ID)
	if err != nil {
		t.Fatalf("the sent invocation is not in Postgres: %v", err)
	}
	if inv.AppID != target.ID || inv.Source != state.InvocationAsyncInvoke || inv.Path != "/users" {
		t.Fatalf("invocation = app %s source %s path %s, want an async invocation of %s at /users", inv.AppID, inv.Source, inv.Path, target.ID)
	}
	var stored map[string]string
	if err := json.Unmarshal(inv.Headers, &stored); err != nil {
		t.Fatalf("decode stored headers: %v", err)
	}
	if _, kept := stored[api.FunctionInvokeHeader]; kept || stored[api.FunctionCallHeader] != "1" {
		t.Fatalf("stored headers = %v: the invoke marker must be dropped and the function-call marker kept", stored)
	}

	// The same idempotency key returns the same invocation instead of a second one.
	status, _, body = serviceCallWithMethod(t, f.h, f.app.ID, "sendusers", http.MethodPost, "/users", send)
	var again api.AsyncInvokeResponse
	if status != http.StatusAccepted || json.Unmarshal([]byte(body), &again) != nil || again.ID != accepted.ID {
		t.Fatalf("repeated send = %d %s, want the same invocation %s", status, body, accepted.ID)
	}

	// Without the marker the call is forwarded to the target as before.
	if status, _, body := pollServiceCall(t, f.h, f.app.ID, "sendusers", "/", http.StatusOK, 30*time.Second); status != http.StatusOK {
		t.Fatalf("plain call status = %d, want the target's 200; body=%s", status, body)
	}
}
