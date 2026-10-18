// adr: 20261017210000
package mcpmanagement

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRegressionTriageCodexSubprocess(t *testing.T) {
	base := os.Getenv("GREGALE_TEST_TRIAGE_API")
	if base == "" {
		t.Skip("isolated Codex fixture subprocess only")
	}
	store, err := OpenStateStore(os.Getenv("GREGALE_TEST_TRIAGE_STATE"), os.Getenv("GREGALE_TEST_TRIAGE_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(api.NewClient(base, testToken), Options{APIBase: base, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "codex-triage"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Run(context.Background(), &mcp.StdioTransport{})
	_ = store.Close()
	if err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestCodexRegressionTriageAcceptance(t *testing.T) {
	binary := os.Getenv("GREGALE_MCP_CODEX_BINARY")
	if binary == "" {
		t.Skip("set GREGALE_MCP_CODEX_BINARY for real-client acceptance")
	}
	f, b, _ := newTriageFixture(t)
	f.mu.Lock()
	f.lost = true
	f.mu.Unlock()
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	statePath := filepath.Join(dir, "state")
	if err := os.WriteFile(key, bytes.Repeat([]byte{41}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/ops/management-mcp-triage-acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "python3", script, executable, b.URL, binary, statePath, key)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Codex triage acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 4 {
		t.Fatalf("native fixture triage writes=%d, want exactly 4", f.writes)
	}
}
