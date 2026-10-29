// adr: 1618
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestMCPServeSubprocess(t *testing.T) {
	if os.Getenv("GREGALE_MCP_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(main.Run(os.Args[i+1:]))
		}
	}
	os.Exit(1)
}

func TestMCPServeStdioUsesSelectedProfile(t *testing.T) {
	main.SetupConnectionProfiles(t)
	// TestMain recognizes this basename and preserves the helper's HOME.
	// Keep the fixture aligned with its XDG path and the native macOS path.
	sandbox := filepath.Join(t.TempDir(), main.CliTestHomePrefix+"mcp")
	t.Setenv("HOME", sandbox)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(sandbox, ".config"))
	token := main.TestAPIKey('m')
	t.Setenv("FAAS_TOKEN", token)
	t.Setenv("FAAS_API", "")
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("management server used the wrong credential or a mutation")
		}
		switch r.URL.Path {
		case "/v1/account":
			_, _ = w.Write([]byte(`{"id":"staging-account","plan":"free","status":"active"}`))
		case "/v1/capabilities":
			_, _ = w.Write([]byte(`{"plan":"free","capabilities":[]}`))
		default:
			t.Errorf("unexpected management request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer backend.Close()
	if err := main.SaveCLIConfig(main.CliConfig{APIBase: "https://unused.example", Profiles: map[string]main.ConnectionProfile{"staging": {APIBase: backend.URL}}}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The executable is this test binary, not an agent-supplied command.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestMCPServeSubprocess$", "--", "--non-interactive", "--profile", "staging", "--json", "mcp", "serve", "--transport", "stdio") //nolint:gosec // current test executable, fixed arguments
	cmd.Env = append(os.Environ(), "GREGALE_MCP_TEST_HELPER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v; stderr: %s", err, stderr.String())
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(ctx, nil)
	// 49 read tools, the local deploy and env plans (ADR-20261026141841), and
	// the provisioning plans and domain check (ADR-20261026174727).
	if err != nil || len(tools.Tools) != 56 || requests.Load() != 0 {
		t.Fatalf("discovery=%+v error=%v requests=%d", tools, err, requests.Load())
	}
	listed := map[string]bool{}
	for _, tool := range tools.Tools {
		listed[tool.Name] = true
	}
	if !listed["gregale_plan_local_deploy"] || !listed["gregale_plan_env_change"] {
		t.Fatal("a local stdio connection did not list the local deploy and env plans")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_context", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("context=%+v error=%v", result, err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil || !strings.Contains(string(data), `"profile":"staging"`) || !strings.Contains(string(data), "staging-account") || strings.Contains(string(data), token) {
		t.Fatalf("context=%s error=%v", data, err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("stdio shutdown: %v", err)
	}
	if strings.Contains(stderr.String(), token) || requests.Load() != 2 {
		t.Fatalf("unexpected diagnostics or API request count: %d", requests.Load())
	}
}

func TestMCPServeRejectsInvalidStartupWithoutProtocolOutput(t *testing.T) {
	main.SetupConnectionProfiles(t)
	for _, args := range [][]string{
		{"--transport", "http"}, {"--timeout", "0s"}, {"--timeout", "2m"}, {"unexpected"}, {"--unknown"},
	} {
		var stdout bytes.Buffer
		if code := main.CaptureStdoutSwap(t, &stdout, func() int { return main.CmdMCPServe(args) }); code != 1 || stdout.Len() != 0 {
			t.Fatalf("args=%v exit=%d stdout=%q", args, code, stdout.String())
		}
	}
	var stdout bytes.Buffer
	if code := main.CaptureStdoutSwap(t, &stdout, func() int { return main.CmdMCPServe(nil) }); code != 2 || stdout.Len() != 0 {
		t.Fatalf("missing credential: exit=%d stdout=%q", code, stdout.String())
	}
}

func TestMCPServeRemoteNeedsNoHostCredential(t *testing.T) {
	main.SetupConnectionProfiles(t)
	opts, err := main.ParseMCPServeOptions([]string{"--transport", "streamable-http", "--listen", "127.0.0.1:0", "--resource-url", "https://management.example/mcp", "--oauth-redirect-uri", "https://client.example/callback", "--oauth-redirect-uri", "http://127.0.0.1/callback"})
	if err != nil || (*opts.AllowWrites()) || len((*opts.Redirects())) != 2 {
		t.Fatalf("remote options=%+v error=%v", opts, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := main.ServeMCPHTTP(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("remote startup without CLI login=%v", err)
	}
}

func TestMCPServeWriteOptionIsExplicit(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		writes bool
	}{{nil, false}, {[]string{"--allow-writes"}, true}, {[]string{"--allow-writes=false"}, false}} {
		opts, err := main.ParseMCPServeOptions(tc.args)
		if err != nil || (*opts.AllowWrites()) != tc.writes {
			t.Fatalf("write options=%+v error=%v", opts, err)
		}
	}
}

func TestMCPServeRemoteStateAndOnboardingOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--oauth-onboarding", "invalid"},
		{"--state-file", "state"},
		{"--state-key-file", "key"},
		{"--state-file", "state", "--state-key-file", "key"},
		{"--oauth-onboarding", "api-key"},
	} {
		if _, err := main.ParseMCPServeOptions(args); err == nil {
			t.Fatalf("invalid remote options accepted: %v", args)
		}
	}
	opts, err := main.ParseMCPServeOptions([]string{"--transport", "streamable-http", "--state-file", "state", "--state-key-file", "key"})
	if err != nil || (*opts.Onboarding()) != "browser" || (*opts.StateFile()) != "state" || (*opts.AllowWrites()) {
		t.Fatalf("remote state options=%+v err=%v", opts, err)
	}
}
