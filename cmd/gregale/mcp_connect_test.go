package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// mcpConnectFixture fakes the machine `gregale mcp connect` runs on: which
// client binaries exist, what `claude mcp ...` answers, the home directory
// and the Gregale API (ADR-20261109121411).
type mcpConnectFixture struct {
	home       string
	binaries   map[string]bool
	claudeHas  string // URL of an existing Claude Code entry, "" for none
	calls      []string
	keyCreates int
	api        *httptest.Server
}

func newMCPConnectFixture(t *testing.T, binaries ...string) *mcpConnectFixture {
	t.Helper()
	main.SetupCLIRegression(t)
	f := &mcpConnectFixture{home: t.TempDir(), binaries: map[string]bool{}}
	for _, b := range binaries {
		f.binaries[b] = true
	}
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/keys" {
			f.keyCreates++
			var req api.CreateKeyRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if !strings.Contains(strings.Join(req.Scopes, ","), api.ScopeAgent) {
				t.Errorf("minted key scopes = %v, want agent", req.Scopes)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.APIKeyResponse{ID: "key-1", Plaintext: "fp_live_agentsecret"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.api.Close)
	t.Setenv("FAAS_API", f.api.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	oldLook, oldRun, oldInter, oldHome := *main.MCPConnectLookPath, *main.MCPConnectRun, *main.MCPConnectInteractive, *main.MCPConnectHome
	oldCheck, oldCount := *main.MCPEndpointCheck, *main.MCPHeadlessToolCount
	t.Cleanup(func() {
		*main.MCPConnectLookPath, *main.MCPConnectRun, *main.MCPConnectInteractive, *main.MCPConnectHome = oldLook, oldRun, oldInter, oldHome
		*main.MCPEndpointCheck, *main.MCPHeadlessToolCount = oldCheck, oldCount
	})
	*main.MCPConnectLookPath = func(name string) (string, error) {
		if f.binaries[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	*main.MCPConnectRun = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		f.calls = append(f.calls, call)
		switch {
		case strings.HasPrefix(call, "claude mcp get"):
			if f.claudeHas == "" {
				return "No MCP server found", errors.New("exit status 1")
			}
			return "gregale:\n  Type: http\n  URL: " + f.claudeHas + "\n", nil
		case strings.HasPrefix(call, "claude mcp add"):
			for i, a := range args {
				if a == "gregale" && i+1 < len(args) {
					f.claudeHas = args[i+1]
				}
			}
		case strings.HasPrefix(call, "claude mcp remove"):
			f.claudeHas = ""
		}
		return "", nil
	}
	*main.MCPConnectInteractive = func(name string, args ...string) error {
		f.calls = append(f.calls, "interactive "+name+" "+strings.Join(args, " "))
		return nil
	}
	*main.MCPConnectHome = func() (string, error) { return f.home, nil }
	*main.MCPEndpointCheck = func(string) string { return "reachable, asks the agent to sign in" }
	*main.MCPHeadlessToolCount = func(main.MCPConnectOptions, string) int { return 24 }
	return f
}

func (f *mcpConnectFixture) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &stdout, &stderr
	code := main.Run(append([]string{"mcp", "connect"}, args...))
	*main.OsStdout, *main.OsStderr = oldOut, oldErr
	return code, stdout.String(), stderr.String()
}

func (f *mcpConnectFixture) codexConfig(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, ".codex", "config.toml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

func TestMCPConnectCodexKeepsTheRestOfTheConfig(t *testing.T) {
	f := newMCPConnectFixture(t, "codex")
	const before = "# my settings\nmodel = \"o4\"\n\n[mcp_servers.other]\ncommand = \"other\" # keep\n\n[profiles.fast]\nmodel = \"mini\"\n"
	if err := os.MkdirAll(filepath.Join(f.home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, ".codex", "config.toml"), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := f.run(t, "--yes", "--toolsets", "core,operate")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:%s\nstderr:%s", code, out, errOut)
	}
	got := f.codexConfig(t)
	want := before + "\n[mcp_servers.gregale]\nurl = \"" + f.api.URL + "/mcp\"\nhttp_headers = { \"Gregale-MCP-Toolsets\" = \"core,operate\" }\n"
	if got != want {
		t.Fatalf("config =\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(out, "Codex: connected") {
		t.Fatalf("output:\n%s", out)
	}

	if code, out, _ := f.run(t, "--yes", "--toolsets", "core,operate"); code != 0 || !strings.Contains(out, "already connected") {
		t.Fatalf("second run = %d\n%s", code, out)
	}
	if code, out, _ := f.run(t, "--yes"); code != 1 || !strings.Contains(out, "--force") {
		t.Fatalf("a different existing entry = %d\n%s; want a conflict naming --force", code, out)
	}
	if code, _, _ := f.run(t, "--yes", "--force"); code != 0 {
		t.Fatalf("--force = %d", code)
	}
	replaced := f.codexConfig(t)
	if !strings.HasPrefix(replaced, before) || strings.Contains(replaced, "Gregale-MCP-Toolsets") || strings.Count(replaced, "[mcp_servers.gregale]") != 1 {
		t.Fatalf("after --force:\n%s", replaced)
	}
}

func TestMCPConnectCodexTableInTheMiddle(t *testing.T) {
	const before = "[a]\nx = 1\n\n[mcp_servers.gregale]\nurl = \"https://old/mcp\"\n\n[mcp_servers.gregale.env]\nK = \"v\"\n\n[z]\ny = 2\n"
	block := "[mcp_servers.gregale]\nurl = \"https://new/mcp\"\n"
	if _, state := main.UpsertCodexServer(before, block, false); state != "conflict" {
		t.Fatalf("state = %s, want conflict", state)
	}
	got, state := main.UpsertCodexServer(before, block, true)
	if state != "replaced" || got != "[a]\nx = 1\n\n"+block+"\n[z]\ny = 2\n" {
		t.Fatalf("replace = %s %q", state, got)
	}
}

func TestMCPConnectClaudeCode(t *testing.T) {
	f := newMCPConnectFixture(t, "claude")
	code, out, errOut := f.run(t, "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	wantAdd := "claude mcp add --transport http --scope user gregale " + f.api.URL + "/mcp"
	if !containsCall(f.calls, wantAdd) {
		t.Fatalf("calls = %v; want %q", f.calls, wantAdd)
	}
	if !strings.Contains(out, "Claude Code: connected") || !strings.Contains(out, "/mcp") {
		t.Fatalf("output:\n%s", out)
	}
	if f.codexConfig(t) != "" {
		t.Fatal("Codex was not detected but its config was written")
	}
	f.calls = nil
	if code, out, _ := f.run(t, "--yes"); code != 0 || !strings.Contains(out, "already connected") || containsPrefix(f.calls, "claude mcp add") {
		t.Fatalf("second run = %d %v\n%s", code, f.calls, out)
	}
}

func TestMCPConnectHeadlessProjectDryRun(t *testing.T) {
	f := newMCPConnectFixture(t, "claude", "codex")
	code, out, _ := f.run(t, "--client", "claude-code", "--project", "--dry-run")
	if code != 0 || !strings.Contains(out, "would connect") || containsPrefix(f.calls, "claude mcp add") || !strings.Contains(out, "--scope project") {
		t.Fatalf("dry run = %d %v\n%s", code, f.calls, out)
	}
	if f.keyCreates != 0 {
		t.Fatal("a dry run minted a key")
	}

	code, out, errOut := f.run(t, "--client", "claude-code,codex", "--headless")
	if code != 0 {
		t.Fatalf("headless exit %d\n%s\n%s", code, out, errOut)
	}
	if f.keyCreates != 1 {
		t.Fatalf("minted %d keys, want 1 for both clients", f.keyCreates)
	}
	if !containsCall(f.calls, "--header Authorization: Bearer ${GREGALE_AGENT_KEY}") {
		t.Fatalf("Claude Code header missing: %v", f.calls)
	}
	codex := f.codexConfig(t)
	if !strings.Contains(codex, "bearer_token_env_var = \"GREGALE_AGENT_KEY\"") || strings.Contains(codex, "fp_live_agentsecret") {
		t.Fatalf("codex config:\n%s", codex)
	}
	if containsPrefix(f.calls, "interactive codex mcp login") {
		t.Fatal("headless ran the browser login")
	}
	if strings.Count(out, "fp_live_agentsecret") != 1 || !strings.Contains(out, "export GREGALE_AGENT_KEY=") || !strings.Contains(out, "24 tools") {
		t.Fatalf("headless output:\n%s", out)
	}
}

func TestMCPConnectRefusals(t *testing.T) {
	f := newMCPConnectFixture(t)
	if code, _, errOut := f.run(t, "--yes"); code != 1 || !strings.Contains(errOut, "Claude Code") {
		t.Fatalf("no client found = %d\n%s", code, errOut)
	}
	if code, _, errOut := f.run(t, "--client", "cursor"); code != 1 || !strings.Contains(errOut, "claude-code") {
		t.Fatalf("unknown client = %d\n%s", code, errOut)
	}
	if code, out, _ := f.run(t, "--client", "codex", "--project"); code != 0 || !strings.Contains(out, "skipped") {
		t.Fatalf("codex --project = %d\n%s", code, out)
	}
}

func containsCall(calls []string, part string) bool {
	for _, c := range calls {
		if strings.Contains(c, part) {
			return true
		}
	}
	return false
}

func containsPrefix(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}
