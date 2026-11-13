package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPRegressionReviewedSourceContentChange(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	cs, ctx := f.connect(t, true)
	dir := projectDir(t)
	path := filepath.Join(dir, "server.js")
	before, _ := os.Stat(path)
	original, _ := os.ReadFile(path)
	plan := callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"app": "alpha", "path": dir, "reason": "review original code"}, false)
	replacement := strings.Repeat("X", len(original))
	if err := os.WriteFile(path, []byte(replacement), before.Mode()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_local_deploy", Arguments: map[string]any{"plan_id": planID(t, plan)}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("changed source was deployed under the original plan; CLI deploy calls=%d", len(f.deployCalls()))
	}
}

func TestMCPRegressionUploadRecoveryRouting(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	uploads, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://example.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	cs, ctx := f.connect(t, true, func(o *Options) {
		o.uploads = uploads
		o.grantID = "one"
		o.agentKey = true
		o.Toolsets = []string{ToolsetAll}
	})
	planned := callTool(t, ctx, cs, "gregale_plan_upload_deploy", map[string]any{"app": "alpha", "reason": "upload source", "files": []map[string]any{{"path": "server.js", "content": "original"}}}, false)
	id := planID(t, planned)
	f.mu.Lock()
	f.failAfter = true
	f.mu.Unlock()
	first, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_upload_deploy", Arguments: map[string]any{"plan_id": id}})
	if err != nil || !first.IsError {
		t.Fatalf("expected ambiguous CLI error, result=%+v err=%v", first, err)
	}
	receipt := callTool(t, ctx, cs, "gregale_get_plan", map[string]any{"plan_id": id}, false)
	var recovery Recovery
	if err := json.Unmarshal(receipt["recovery"], &recovery); err != nil {
		t.Fatal(err)
	}
	if recovery.Tool == "" {
		t.Fatalf("no recovery: %s", receipt["recovery"])
	}
	replay, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: recovery.Tool, Arguments: recovery.Arguments})
	if err != nil {
		t.Fatal(err)
	}
	if replay.IsError {
		t.Fatalf("receipt recommends %s but it refuses the upload plan: %+v", recovery.Tool, replay.StructuredContent)
	}
}

func TestMCPRegressionDurableProgressStreaming(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := progressFixture()
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	s, err := NewServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	r := &remoteServer{opts: HTTPOptions{StateStore: store}}
	endpoint := httptest.NewServer(r.durableHandler(newWatchHTTPHandler(func(*http.Request) *mcp.Server { return s })))
	defer endpoint.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	progress := make(chan struct{}, 8)
	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "1"}, &mcp.ClientOptions{ProgressNotificationHandler: func(context.Context, *mcp.ProgressNotificationClientRequest) { progress <- struct{}{} }})
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint.URL, DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = cs.CallTool(ctx, watchParams("probe", 1)) }()
	select {
	case <-progress:
	case <-time.After(400 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("no progress reaches the client during the watch with durable state enabled")
	}
	<-done
}

func TestMCPRegressionDurableCommitFailureWithholdsResponses(t *testing.T) {
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			persist := &countingPersistence{err: errors.New("storage unavailable")}
			store, err := openStateStore(persist)
			if err != nil {
				t.Fatal(err)
			}
			r := &remoteServer{opts: HTTPOptions{StateStore: store}}
			response := httptest.NewRecorder()
			r.durableHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write([]byte("uncommitted-response"))
				_ = http.NewResponseController(w).Flush()
			})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/mcp", nil))
			if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "uncommitted-response") {
				t.Fatalf("uncommitted response escaped: status=%d body=%s", response.Code, response.Body.String())
			}
			if store.available() || persist.commits != 1 {
				t.Fatalf("failed persistence left store available=%v, commits=%d", store.available(), persist.commits)
			}
		})
	}
}

func TestMCPRegressionDurableUploadReadDeadline(t *testing.T) {
	for _, durable := range []bool{false, true} {
		name := "without_state"
		if durable {
			name = "with_state"
		}
		t.Run(name, func(t *testing.T) {
			u, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://example.test/mcp")
			if err != nil {
				t.Fatal(err)
			}
			_, uploadURL, err := u.create("grant", "account", "alpha", 1000)
			if err != nil {
				t.Fatal(err)
			}
			r := &remoteServer{}
			if durable {
				r.opts.StateStore, _, _ = testStateStore(t)
			}
			server := httptest.NewUnstartedServer(r.durableHandler(http.HandlerFunc(u.receive)))
			server.Config.ReadTimeout = 50 * time.Millisecond
			server.Start()
			defer server.Close()
			reader, writer := io.Pipe()
			go func() {
				defer writer.Close()
				_, _ = writer.Write([]byte("a"))
				time.Sleep(120 * time.Millisecond)
				_, _ = writer.Write([]byte("b"))
			}()
			response, err := http.Post(server.URL+strings.TrimPrefix(uploadURL, "https://example.test"), "application/octet-stream", reader)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != 201 {
				t.Fatalf("slow upload failed with status %d: %s", response.StatusCode, body)
			}
		})
	}
}

func TestMCPRegressionLongHTTPApplyReceipt(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	uploads, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://example.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	cli := func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
		if len(args) > 0 && args[0] == "deploy" && !strings.Contains(strings.Join(args, " "), "--plan") {
			time.Sleep(150 * time.Millisecond)
		}
		return f.cli(ctx, args, stdin)
	}
	s, err := NewServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetAll}, LocalCLI: cli, uploads: uploads, agentKey: true, grantID: "one"})
	if err != nil {
		t.Fatal(err)
	}
	store, _, _ := testStateStore(t)
	remote := &remoteServer{opts: HTTPOptions{StateStore: store}}
	endpoint := httptest.NewUnstartedServer(remote.durableHandler(newWatchHTTPHandler(func(*http.Request) *mcp.Server { return s })))
	// Scale the production 75-second HTTP deadline down while preserving its
	// mismatch with the longer per-tool deployment deadline.
	endpoint.Config.WriteTimeout = 75 * time.Millisecond
	endpoint.Start()
	defer endpoint.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "timeout-probe", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint.URL, DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	plan := callTool(t, ctx, cs, "gregale_plan_upload_deploy", map[string]any{"app": "alpha", "reason": "slow CLI", "files": []map[string]any{{"path": "server.js", "content": "test"}}}, false)
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_upload_deploy", Arguments: map[string]any{"plan_id": planID(t, plan)}})
	if err != nil {
		t.Fatalf("apply ran %d CLI write(s) but no receipt reached the client: %v", len(f.deployCalls()), err)
	}
	if result.IsError {
		t.Fatalf("apply failed: %+v", result.StructuredContent)
	}
}
