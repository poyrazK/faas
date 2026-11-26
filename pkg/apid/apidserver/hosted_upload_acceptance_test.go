//go:build !no_pg

package apidserver_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/mcpmanagement"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostedUploadNativeAcceptance(t *testing.T) {
	cli, python := os.Getenv("MCP_UPLOAD_TEST_GREGALE"), os.Getenv("MCP_UPLOAD_TEST_PYTHON")
	if cli == "" && python == "" {
		t.Skip("set MCP_UPLOAD_TEST_GREGALE and MCP_UPLOAD_TEST_PYTHON for the native upload rehearsal")
	}
	if !filepath.IsAbs(cli) || !filepath.IsAbs(python) {
		t.Fatal("the native upload rehearsal requires absolute paths to Gregale and Python with FastAPI and Uvicorn")
	}
	ctx := t.Context()
	spool := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	pool := pgtest.OpenMigrated(t)
	mustHostedTaskAcceptance(t, db.MigrateUp(ctx, pool))
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "hosted-upload-"+uuid.NewString()+"@example.com", api.PlanPro)
	mustHostedTaskAcceptance(t, err)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "fastapi", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
	mustHostedTaskAcceptance(t, err)
	token, hash, err := api.GenerateAPIKey()
	mustHostedTaskAcceptance(t, err)
	_, err = store.CreateAPIKey(ctx, account.ID, hash, "upload acceptance", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	mustHostedTaskAcceptance(t, err)

	// The portable harness runs the response-check protocol and executes the
	// committed Python source. It supplies the scheduler's live transition and
	// the gateway's routing to that process without a Firecracker host.
	gatewayMux := http.NewServeMux()
	gatewayMux.HandleFunc("GET "+api.DeploymentResponseCheckCapabilitiesControlPath, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion, Available: true})
	})
	gateway := httptest.NewServer(gatewayMux)
	t.Cleanup(gateway.Close)
	handler := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
		WithGatewaydControlURL(gateway.URL).Handler()
	var starts, chunks, commits atomic.Int32
	apid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads":
			starts.Add(1)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/uploads/"):
			chunks.Add(1)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/uploads/") && strings.HasSuffix(r.URL.Path, "/commit"):
			commits.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(apid.Close)
	agent := api.NewClient(apid.URL, token)

	uploadDir, keyPath := t.TempDir(), filepath.Join(t.TempDir(), "state.key")
	key := make([]byte, 32)
	_, err = rand.Read(key)
	mustHostedTaskAcceptance(t, err)
	mustHostedTaskAcceptance(t, os.WriteFile(keyPath, key, 0o600))
	stateOptions := mcpmanagement.PostgresStateOptions{Pool: pool, Bridge: "hosted-upload-acceptance", KeyPath: keyPath}
	var bridgeAddress string
	connect := func() (*mcp.ClientSession, *httptest.Server, func()) {
		journal, err := mcpmanagement.OpenPostgresStateStore(ctx, stateOptions)
		mustHostedTaskAcceptance(t, err)
		t.Cleanup(func() { mustHostedTaskAcceptance(t, journal.Close()) })
		bridge := httptest.NewUnstartedServer(nil)
		if bridgeAddress == "" {
			bridgeAddress = bridge.Listener.Addr().String()
		} else {
			// The stored endpoint binding must stay constant across the restart.
			mustHostedTaskAcceptance(t, bridge.Listener.Close())
			bridge.Listener, err = net.Listen("tcp", bridgeAddress)
			mustHostedTaskAcceptance(t, err)
		}
		t.Cleanup(bridge.Close)
		endpoint := "https://" + bridgeAddress + "/mcp"
		bridge.Config.Handler, err = mcpmanagement.NewHTTPHandler(mcpmanagement.HTTPOptions{
			APIBase: apid.URL, ResourceURL: endpoint, RedirectURIs: []string{"http://127.0.0.1/callback"},
			Hosted: true, AgentKeys: true, AgentKeyToolsetSelection: true, AllowWrites: true,
			Toolsets: []string{mcpmanagement.ToolsetCore}, UploadDir: uploadDir, StateStore: journal,
			CLIRunner: mcpmanagement.HostedCLIRunner(cli, apid.URL, uploadDir),
		})
		mustHostedTaskAcceptance(t, err)
		bridge.StartTLS()
		client := *bridge.Client()
		client.Transport = hostedTaskAgentTransport{base: client.Transport, token: token, toolset: mcpmanagement.ToolsetCore}
		session, err := mcp.NewClient(&mcp.Implementation{Name: "hosted-upload-acceptance", Version: "1"}, nil).
			Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &client, DisableStandaloneSSE: true},
				&mcp.ClientSessionOptions{ProtocolVersion: api.MCPProtocolVersion})
		mustHostedTaskAcceptance(t, err)
		t.Cleanup(func() { _ = session.Close() })
		return session, bridge, func() {
			mustHostedTaskAcceptance(t, session.Close())
			bridge.Close()
			mustHostedTaskAcceptance(t, journal.Close())
		}
	}

	first, bridge, stop := connect()
	source := uploadHostedTaskSource(t, first, bridge, app.Slug, fastAPIUploadArchive(t))
	type planReceipt struct {
		ID        string `json:"plan_id"`
		ExpiresAt string `json:"expires_at"`
	}
	var plan planReceipt
	getHostedAcceptanceTool(t, first, "gregale_plan_upload_deploy", map[string]any{"app": app.Slug, "upload_id": source["upload_id"],
		"reason": "native hosted FastAPI restart acceptance"}, &plan)
	if plan.ID == "" {
		t.Fatal("deployment plan omitted its ID")
	}
	applyArgs := map[string]any{"plan_id": plan.ID}
	pending, err := first.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_upload_deploy", Arguments: applyArgs})
	mustHostedTaskAcceptance(t, err)
	var request struct {
		Code     string                       `json:"code"`
		Approval api.AgentApprovalPendingInfo `json:"approval"`
	}
	raw, err := json.Marshal(pending.StructuredContent)
	mustHostedTaskAcceptance(t, err)
	mustHostedTaskAcceptance(t, json.Unmarshal(raw, &request))
	if !pending.IsError || request.Code != api.CodeApprovalRequired || request.Approval.ID == "" || request.Approval.ReviewURL == "" {
		t.Fatalf("first apply did not return the pending approval: %s", raw)
	}
	approval, err := agent.GetAgentApproval(ctx, request.Approval.ID)
	mustHostedTaskAcceptance(t, err)
	uploadID := strings.TrimSuffix(strings.TrimPrefix(approval.Path, "/v1/uploads/"), "/commit")
	if approval.Method != http.MethodPost || !approval.BodyBound || approval.Status != api.AgentApprovalPending ||
		approval.Path != "/v1/uploads/"+uploadID+"/commit" || uploadID == "" {
		t.Fatalf("approval was not bound to an upload commit: %+v", approval)
	}
	upload, err := agent.GetUploadSession(ctx, uploadID)
	mustHostedTaskAcceptance(t, err)
	if upload.AppSlug != app.Slug || upload.Status != "open" || upload.ReceivedBytes == 0 || upload.ReceivedBytes != upload.TotalSize {
		t.Fatalf("native upload was not complete and pending: %+v", upload)
	}
	chunkCount := chunks.Load()
	if starts.Load() != 1 || chunkCount < 1 || commits.Load() != 1 {
		t.Fatal("first apply did not stage one native upload before requesting approval")
	}

	// Discard the first listener, MCP session, and in-memory journal. The
	// replacement has only the PostgreSQL journal and the original upload directory.
	stop()
	second, _, _ := connect()
	var recovered planReceipt
	getHostedAcceptanceTool(t, second, "gregale_get_plan", applyArgs, &recovered)
	assertHostedTaskEqual(t, plan.ID, recovered.ID)
	assertHostedTaskEqual(t, approval.ExpiresAt, recovered.ExpiresAt)
	var waiting struct {
		ID        string `json:"approval_id"`
		Status    string `json:"status"`
		ReviewURL string `json:"review_url"`
		ExpiresAt string `json:"expires_at"`
	}
	waitArgs := map[string]any{"approval_id": approval.ID, "wait_seconds": 1}
	getHostedAcceptanceTool(t, second, "gregale_wait_for_approval", waitArgs, &waiting)
	assertHostedTaskEqual(t, approval.ID, waiting.ID)
	assertHostedTaskEqual(t, approval.Status, waiting.Status)
	assertHostedTaskEqual(t, request.Approval.ReviewURL, waiting.ReviewURL)
	assertHostedTaskEqual(t, approval.ExpiresAt, waiting.ExpiresAt)
	stillOpen, err := agent.GetUploadSession(ctx, uploadID)
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, upload, stillOpen)

	// Use the same owner decision the dashboard records for this fixture account.
	mustHostedTaskAcceptance(t, store.DecideAgentApproval(ctx, approval.ID, true, account.ID, "acceptance"))
	getHostedAcceptanceTool(t, second, "gregale_wait_for_approval", waitArgs, &waiting)
	assertHostedTaskEqual(t, api.AgentApprovalApproved, waiting.Status)
	var applied struct {
		Deployment api.DeploymentResponse `json:"deployment"`
	}
	getHostedAcceptanceTool(t, second, "gregale_apply_upload_deploy", applyArgs, &applied)
	if !api.ValidDeploymentResponseCheckID(applied.Deployment.ID) {
		t.Fatal("retry did not return an exact deployment ID")
	}
	outcome, err := store.GetUploadCommitOutcome(ctx, uploadID)
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, applied.Deployment.ID, outcome.DeploymentID)
	consumed, err := agent.GetAgentApproval(ctx, approval.ID)
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, api.AgentApprovalConsumed, consumed.Status)
	if starts.Load() != 1 || chunks.Load() != chunkCount || commits.Load() != 2 {
		t.Fatalf("retry changed native upload traffic: starts=%d chunks=%d (original %d) commits=%d", starts.Load(), chunks.Load(), chunkCount, commits.Load())
	}
	deployments, err := store.ListDeploymentsForApp(ctx, app.ID, 2, 0)
	mustHostedTaskAcceptance(t, err)
	if len(deployments) != 1 || deployments[0].ID != outcome.DeploymentID {
		t.Fatalf("expected precisely the deployment committed by %s, got %v", uploadID, deployments)
	}

	committed, err := store.GetUploadSession(ctx, uploadID)
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, "committed", committed.Status)
	assertHostedTaskEqual(t, outcome.DeploymentID, committed.DeploymentID.String)
	runtimeURL := startCommittedFastAPI(t, python, spool, committed.PartPath)
	const targetPath = "/items/5?q=gregale"
	gatewayMux.HandleFunc("POST /v1/internal/apps/"+app.Slug+"/response-check", func(w http.ResponseWriter, r *http.Request) {
		var target api.DeploymentResponseCheckRequest
		if json.NewDecoder(r.Body).Decode(&target) != nil || target.AppID != app.ID || target.Path != targetPath ||
			r.URL.Query().Get("deployment_id") != outcome.DeploymentID {
			http.Error(w, "unexpected deployment target", http.StatusConflict)
			return
		}
		probe, err := http.NewRequestWithContext(r.Context(), http.MethodGet, runtimeURL+target.Path, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response, err := http.DefaultClient.Do(probe)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, api.DeploymentResponseCheckMaxBodyBytes+1))
		if err != nil || len(body) > api.DeploymentResponseCheckMaxBodyBytes {
			http.Error(w, "invalid application body", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckResult{AppID: app.ID, DeploymentID: outcome.DeploymentID,
			Scope: target.Scope, InstanceID: uuid.NewString(), HTTPStatus: response.StatusCode, Body: string(body)})
	})
	mustHostedTaskAcceptance(t, store.MarkDeploymentLive(ctx, outcome.DeploymentID))
	var verified struct {
		api.DeploymentResponseCheckResult
		Path                 string `json:"path"`
		Verified             bool   `json:"verified"`
		PublicAccessVerified bool   `json:"public_access_verified"`
	}
	getHostedAcceptanceTool(t, second, "gregale_verify_application_response", map[string]any{
		"app": app.Slug, "deployment_id": outcome.DeploymentID, "path": targetPath,
	}, &verified)
	if !verified.Verified || verified.PublicAccessVerified || verified.AppID != app.ID ||
		verified.DeploymentID != outcome.DeploymentID || verified.Path != targetPath || verified.HTTPStatus != http.StatusOK || verified.BodyTruncated {
		t.Fatalf("incorrect exact deployment response evidence: %+v", verified)
	}
	var body map[string]any
	mustHostedTaskAcceptance(t, json.Unmarshal([]byte(verified.Body), &body))
	assertHostedTaskEqual(t, map[string]any{"item_id": float64(5), "q": "gregale"}, body)
}

func fastAPIUploadArchive(t *testing.T) []byte {
	t.Helper()
	files := map[string][]byte{"gregale.yaml": []byte("hosting:\n  start: uvicorn main:app --host 0.0.0.0 --port $PORT\n  health: /\n")}
	headers := []tar.Header{{Name: "gregale.yaml"}}
	for _, name := range []string{"main.py", "requirements.txt"} {
		contents, err := os.ReadFile(filepath.Join("..", "..", "frameworkprofile", "testdata", "fastapi-hello-world", name))
		mustHostedTaskAcceptance(t, err)
		files[name] = contents
		headers = append(headers, tar.Header{Name: name})
	}
	return apidserver.BuildTestTarGz(t, headers, files)
}

func startCommittedFastAPI(t *testing.T, python, spool, archivePath string) string {
	t.Helper()
	dir := t.TempDir()
	relativeArchive, err := filepath.Rel(spool, archivePath)
	mustHostedTaskAcceptance(t, err)
	file, err := os.OpenInRoot(spool, relativeArchive)
	mustHostedTaskAcceptance(t, err)
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("open committed gzip archive: %v", err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	var entries []string
	for {
		entry, err := archive.Next()
		if err == io.EOF {
			t.Fatalf("committed archive has no main.py: %v", entries)
		}
		mustHostedTaskAcceptance(t, err)
		entries = append(entries, entry.Name)
		if entry.Typeflag == tar.TypeReg && path.Base(entry.Name) == "main.py" {
			code, err := io.ReadAll(archive)
			mustHostedTaskAcceptance(t, err)
			mustHostedTaskAcceptance(t, os.WriteFile(filepath.Join(dir, "main.py"), code, 0o600))
			break
		}
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	mustHostedTaskAcceptance(t, err)
	defer listener.Close()
	socket, err := listener.File()
	mustHostedTaskAcceptance(t, err)
	defer socket.Close()
	logs, err := os.Create(filepath.Join(dir, "uvicorn.log"))
	mustHostedTaskAcceptance(t, err)
	t.Cleanup(func() { _ = logs.Close() })
	cmd := exec.CommandContext(t.Context(), python, "-c", `import socket, uvicorn
from main import app
uvicorn.Server(uvicorn.Config(app)).run(sockets=[socket.socket(fileno=3)])`)
	cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.ExtraFiles = dir, logs, logs, []*os.File{socket}
	mustHostedTaskAcceptance(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() })
	base := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for deadline.Err() == nil {
		req, err := http.NewRequestWithContext(deadline, http.MethodGet, base+"/", nil)
		mustHostedTaskAcceptance(t, err)
		if response, err := client.Do(req); err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return base
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	output, _ := os.ReadFile(logs.Name())
	t.Fatalf("committed FastAPI source did not start: %s", output)
	return ""
}
