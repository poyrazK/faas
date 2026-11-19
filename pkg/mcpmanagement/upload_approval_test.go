// adr: 20261117174831
package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestHostedUploadApprovalWindow(t *testing.T) {
	for _, tc := range []struct {
		name, code string
	}{
		{name: "approved after original plan and directory expiry"},
		{name: "new app created before approval"},
		{name: "approval for an earlier deploy write"},
		{name: "accepted upload recovered after a lost reply"},
		{name: "app replaced", code: "plan_stale"},
		{name: "release changed", code: "plan_stale"},
		{name: "upload replaced", code: "plan_stale"},
		{name: "approval changed", code: "plan_stale"},
		{name: "denied", code: api.CodeApprovalDenied},
		{name: "expired", code: api.CodeApprovalExpired},
		{name: "approval consumed without an accepted release", code: "upload_outcome_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				testHostedUploadApprovalWindow(t, tc.name, tc.code)
			})
		})
	}
}

func testHostedUploadApprovalWindow(t *testing.T, scenario, wantCode string) {
	t.Helper()
	const uploadID = "original-upload"
	const verifyPath = "/items/5?q=gregale"
	f := newLocalFixture(t, scenario != "new app created before approval", "")
	f.app.ID = applicationResponseAppID
	response := newApplicationResponseFixture(t)
	response.path, response.result.Body = verifyPath, `{"item_id":5,"q":"gregale"}`
	store, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.gregale.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	approval := api.AgentApprovalResponse{
		ID: "approval-1", Category: api.AgentApprovalProduction, Method: http.MethodPost, Path: "/v1/uploads/" + uploadID + "/commit",
		BodyBound: true, Status: api.AgentApprovalPending, ReviewURL: "https://gregale.test/approve",
	}
	if scenario == "approval for an earlier deploy write" {
		approval.Path = "/v1/apps/alpha"
	}
	upload := api.UploadSessionResponse{UploadID: uploadID, AppSlug: "alpha", Status: "open", ReceivedBytes: 10, TotalSize: 10}
	client := api.NewClient("http://native.test", testToken).SetCompletionCache(nil)
	client.HTTPClient().Transport = leaseRoundTripper(func(req *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/agent-approvals/" + approval.ID:
			_ = json.NewEncoder(w).Encode(approval)
		case "/v1/uploads/" + uploadID:
			_ = json.NewEncoder(w).Encode(upload)
		case "/v1/deployments/response-check/capabilities", "/v1/apps/alpha/deployments/" + applicationResponseDeploymentID + "/response-check":
			response.serveHTTP(w, req)
		default:
			f.serveHTTP(w, req)
		}
		return w.Result(), nil
	})
	var attempts, commits int
	commitUpload := func() {
		commits++
		approval.Status, upload.Status = api.AgentApprovalConsumed, "committed"
		id := applicationResponseDeploymentID
		upload.DeploymentID, f.latest, f.current = &id, id, id
		f.deployments[id] = response.deployment
	}
	var argsOnFirstApply []string
	cli := func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
		if slices.Contains(args, "--plan") {
			return f.cli(ctx, args, stdin)
		}
		attempts++
		if attempts == 1 {
			argsOnFirstApply = slices.Clone(args)
			f.exists = true
			approval.ExpiresAt = time.Now().Add(api.AgentApprovalTTL).UTC().Format(time.RFC3339Nano)
			p := api.NewProblem(428, api.CodeApprovalRequired, "Approval required", "Review the upload")
			p.AgentApproval = &api.AgentApprovalPendingInfo{ID: approval.ID, Category: approval.Category, ReviewURL: approval.ReviewURL, ExpiresAt: approval.ExpiresAt}
			data, _ := json.Marshal(p)
			return LocalCLIResult{Stderr: data, ExitCode: 1}, nil
		}
		if approval.Status != api.AgentApprovalApproved || !slices.Equal(args, append(slices.Clone(argsOnFirstApply), "--resume-upload-id", uploadID)) {
			t.Errorf("retry did not use its original approved upload and deploy flags: %v", args)
		}
		commitUpload()
		return LocalCLIResult{Stdout: []byte(`{"id":"` + applicationResponseDeploymentID + `"}`)}, nil
	}
	server, manager, err := newManagementServer(client, Options{
		APIBase: client.BaseURL(), Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetCore},
		LocalCLI: cli, uploads: store, grantID: "grant-one", Environments: &EnvironmentConsent{DefaultWrite: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "upload-approval", Version: "1"}, nil).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	plan := callTool(t, ctx, cs, "gregale_plan_upload_deploy", map[string]any{
		"app": "alpha", "reason": "verify an approved upload",
		"files": []inlineFile{{Path: "main.py", Content: "from fastapi import FastAPI\napp = FastAPI()\n"}},
	}, false)
	id := planID(t, plan)
	args := applyArgs{PlanID: id}
	// Open the native approval near the end of the initial plan window.
	time.Sleep(14 * time.Minute)
	first := callTool(t, ctx, cs, "gregale_apply_upload_deploy", args, true)
	if toolCode(t, first) != api.CodeApprovalRequired {
		t.Fatalf("first apply = %v", first)
	}
	got := callTool(t, ctx, cs, "gregale_get_plan", args, false)
	if scenario == "approval for an earlier deploy write" {
		if string(got["expires_at"]) != string(plan["expires_at"]) {
			t.Fatalf("approval on a different write extended the plan: %v", got)
		}
		return
	}
	if string(got["expires_at"]) != `"`+approval.ExpiresAt+`"` {
		t.Fatalf("plan and approval deadlines differ: %s vs %s", got["expires_at"], approval.ExpiresAt)
	}

	// Seventeen minutes after the approval request, both original expiry
	// windows have passed. Exercise the actual pruning and idle checks.
	time.Sleep(17 * time.Minute)
	manager.mu.Lock()
	manager.pruneReleasePlans()
	p := manager.plans[id]
	manager.mu.Unlock()
	store.mu.Lock()
	store.pruneLocked()
	store.mu.Unlock()
	if p == nil || manager.idle(time.Now()) {
		t.Fatal("the waiting plan did not keep its hosted connection")
	}
	if _, err := os.Stat(p.LocalSource.Path); err != nil {
		t.Fatalf("the staged source was pruned while approval was pending: %v", err)
	}
	pending := callTool(t, ctx, cs, "gregale_apply_upload_deploy", args, true)
	if toolCode(t, pending) != api.CodeApprovalRequired || attempts != 1 || string(pending["approval"]) == "" {
		t.Fatalf("pending retry ran the CLI or lost its approval: %v", pending)
	}
	decision := callTool(t, ctx, cs, waitForApprovalTool, waitForApprovalArgs{ApprovalID: approval.ID, WaitSeconds: 1}, false)
	if string(decision["status"]) != `"pending"` || string(decision["expires_at"]) != string(got["expires_at"]) {
		t.Fatalf("wait returned a different approval deadline: %v", decision)
	}
	approval.Status = api.AgentApprovalApproved
	wantAttempts := 2
	switch scenario {
	case "accepted upload recovered after a lost reply":
		commitUpload()
		wantAttempts = 1
	case "app replaced":
		f.app.ID = applicationResponseInstanceID
	case "release changed":
		f.latest = "another-deployment"
	case "upload replaced":
		upload.UploadID = "another-upload"
	case "approval changed":
		approval.Path = "/v1/uploads/another-upload/commit"
	case "denied":
		approval.Status = api.AgentApprovalDenied
	case "expired":
		time.Sleep(13 * time.Minute)
	case "approval consumed without an accepted release":
		approval.Status = api.AgentApprovalConsumed
	}
	if wantCode != "" {
		refused := callTool(t, ctx, cs, "gregale_apply_upload_deploy", args, true)
		if toolCode(t, refused) != wantCode || attempts != 1 || commits != 0 {
			t.Fatalf("unsafe retry for %s: %v, %d attempts, %d commits", scenario, refused, attempts, commits)
		}
		return
	}
	for range 2 {
		accepted := callTool(t, ctx, cs, "gregale_apply_upload_deploy", args, false)
		var dep api.DeploymentResponse
		if err := json.Unmarshal(accepted["deployment"], &dep); err != nil || dep.ID != applicationResponseDeploymentID {
			t.Fatalf("apply returned the wrong release: %+v (%v)", dep, err)
		}
	}
	verified := callTool(t, ctx, cs, "gregale_verify_application_response", map[string]any{
		"app": "alpha", "deployment_id": applicationResponseDeploymentID, "path": verifyPath,
	}, false)
	if attempts != wantAttempts || commits != 1 || string(verified["verified"]) != "true" || string(verified["http_status"]) != "200" ||
		string(verified["deployment_id"]) != `"`+applicationResponseDeploymentID+`"` {
		t.Fatalf("approval did not lead to one verified release: %v, %d attempts, %d commits", verified, attempts, commits)
	}
}
