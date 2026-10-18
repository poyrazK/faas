// adr: 20261017210000
package mcpmanagement

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

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
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "key")
	statePath := filepath.Join(dir, "state")
	if err := os.WriteFile(key, bytes.Repeat([]byte{41}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/ops/management-mcp-triage-acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	// Durable state belongs to one HTTP bridge, as in the CLI. Codex can open
	// both global and thread clients; separate stdio children cannot share its
	// exclusive journal lease. Reopen the bridge itself between client phases.
	for _, phase := range []string{"lost-response", "recover", "replay"} {
		store, err := OpenStateStore(statePath, key)
		if err != nil {
			t.Fatal(err)
		}
		server, err := NewServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "codex-triage"})
		if err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
		bridge := httptest.NewServer(handler)
		command := exec.CommandContext(t.Context(), "python3", script, phase, bridge.URL, binary, filepath.Join(dir, "client-phase.json"))
		output, err := command.CombinedOutput()
		bridge.CloseClientConnections()
		bridge.Close()
		closeErr := store.Close()
		if err != nil {
			t.Fatalf("Codex triage %s: %v\n%s", phase, err, output)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Log(string(output))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 4 {
		t.Fatalf("native fixture triage writes=%d, want exactly 4", f.writes)
	}
}
