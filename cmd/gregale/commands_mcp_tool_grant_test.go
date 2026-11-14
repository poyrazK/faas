package main_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestMCPToolGrantCLIJourney(t *testing.T) {
	main.SetupConnectionProfiles(t)
	f := granttest.New(t, state.NewMemStore(), "")
	t.Setenv("FAAS_API", f.Native.URL)
	t.Setenv("FAAS_TOKEN", f.Owner.Token())
	id := uuid.NewString()
	args := []string{"--json", "mcp", "tool-grant", "create", "--id", id, "--connection-key", f.AgentKey.ID, "--version", f.PublicationA.ID, "--tool", "visible", "--expires-at", time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}
	output, restore := main.CaptureStdout(t)
	code := main.Run(args)
	restore()
	var created api.ToolGrantRecord
	if code != 0 || json.Unmarshal([]byte(output.String()), &created) != nil || created.ID != id || created.Intent.AllowInvocation {
		t.Fatalf("create output: code=%d %s", code, output.String())
	}
	for _, verb := range []string{"get", "list", "revoke"} {
		args := []string{"--json", "mcp", "tool-grant", verb}
		if verb != "list" {
			args = append(args, "--id", id)
		}
		output, restore := main.CaptureStdout(t)
		code := main.Run(args)
		restore()
		if code != 0 || !json.Valid([]byte(output.String())) || !strings.Contains(output.String(), id) {
			t.Fatalf("%s: code=%d %s", verb, code, output.String())
		}
	}
	retained, err := f.Owner.GetToolGrant(t.Context(), id)
	if err != nil || retained.RevokedAt == nil {
		t.Fatal("CLI revocation was not durable", err)
	}
}

func TestMCPPrivateToolGrantCLI(t *testing.T) {
	main.SetupConnectionProfiles(t)
	p := granttest.NewPrivate(t, state.NewMemStore(), "")
	t.Setenv("FAAS_API", p.Base.Native.URL)
	t.Setenv("FAAS_TOKEN", p.Base.Owner.Token())
	id := uuid.NewString()
	output, restore := main.CaptureStdout(t)
	code := main.Run([]string{"--json", "mcp", "tool-grant", "create", "--id", id, "--connection-key", p.Base.AgentKey.ID, "--version", p.Base.PublicationA.ID, "--tool", "visible", "--expires-at", time.Now().UTC().Add(time.Hour).Format(time.RFC3339), "--allow-invocation", "--credential-secret-ref", "secret:MCP_TOOL_TOKEN"})
	restore()
	var grant api.ToolGrantRecord
	if code != 0 || json.Unmarshal([]byte(output.String()), &grant) != nil || grant.Credential == nil || grant.Intent.CredentialSecretRef != "secret:MCP_TOOL_TOKEN" || strings.Contains(output.String(), p.Token) {
		t.Fatalf("private grant CLI: %d %s", code, output.String())
	}
}
