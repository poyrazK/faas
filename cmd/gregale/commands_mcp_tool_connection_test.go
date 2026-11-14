package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestMCPExecutionConnectionCLI(t *testing.T) {
	main.SetupConnectionProfiles(t)
	f := granttest.NewManaged(t, state.NewMemStore(), "")
	t.Setenv("FAAS_API", f.Private.Base.Native.URL)
	t.Setenv("FAAS_TOKEN", f.Private.Base.Owner.Token())
	config := filepath.Join(t.TempDir(), "execution.json")
	raw, _ := json.Marshal(f.Connection.Intent)
	if err := os.WriteFile(config, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"--json", "mcp", "server", "tool-connection", "create", "--id", id, "--config", config})
	restore()
	var connection api.ToolOAuthConnectionRecord
	if code != 0 || json.Unmarshal([]byte(out.String()), &connection) != nil || connection.ID != id || connection.State != "refresh_required" || strings.Contains(out.String(), "provider-exec-refresh-initial") {
		t.Fatalf("connection CLI: %d %s", code, out.String())
	}
	for _, verb := range []string{"get", "list", "revoke"} {
		args := []string{"--json", "mcp", "server", "tool-connection", verb}
		if verb != "list" {
			args = append(args, "--id", id)
		}
		out, restore := main.CaptureStdout(t)
		code := main.Run(args)
		restore()
		if code != 0 || !json.Valid([]byte(out.String())) || !strings.Contains(out.String(), id) {
			t.Fatalf("connection %s: %d %s", verb, code, out.String())
		}
	}
	grantID := uuid.NewString()
	out, restore = main.CaptureStdout(t)
	code = main.Run([]string{"--json", "mcp", "server", "tool-grant", "create", "--id", grantID, "--connection-key", f.Private.Base.AgentKey.ID, "--version", f.Private.Base.PublicationA.ID, "--tool", "visible", "--expires-at", time.Now().UTC().Add(time.Hour).Format(time.RFC3339), "--allow-invocation", "--credential-connection-id", f.Connection.ID})
	restore()
	var grant api.ToolGrantRecord
	if code != 0 || json.Unmarshal([]byte(out.String()), &grant) != nil || grant.Credential == nil || grant.Credential.ConnectionID != f.Connection.ID {
		t.Fatalf("managed grant CLI: %d %s", code, out.String())
	}
}
