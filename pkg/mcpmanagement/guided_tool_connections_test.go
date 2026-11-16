package mcpmanagement

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestHostedGuidedToolConnection(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			m := granttest.NewGuided(t, state.NewMemStore(), testToken)
			m.Connect(t)
			g := m.Grant(t)
			f := m.Private.Base
			opts := Options{APIBase: f.Native.URL, Token: testToken, AllowToolInvocation: true, AllowPrivateToolInvocation: true, Environments: &EnvironmentConsent{}, Toolsets: []string{ToolsetRegistry, ToolsetInvoke}}
			cs, ctx := connectSelectedTestServer(t, f.Native, opts, protocol)
			status := publicationCall(t, ctx, cs, "gregale_get_tool_connection", toolConnectionArgs{GrantID: g.ID}, false)
			raw, _ := json.Marshal(status)
			if string(status["state"]) != `"ready"` || strings.Contains(string(raw), "authorization_url") || strings.Contains(string(raw), "guided-access-") {
				t.Fatal("guided status exposed owner authorization data")
			}
			args := invokeToolArgs{Version: f.PublicationA.ID, Name: "visible", GrantID: g.ID, CredentialBinding: g.Credential.SHA256, RequestID: uuid.NewString(), AccessMode: "external-oauth", ArgumentsJSON: `{"value":9007199254740993}`, AllowToolExecution: true, TimeoutSeconds: 15}
			invocationCall(t, ctx, cs, "gregale_invoke_tool", args, false)
			var done map[string]json.RawMessage
			for i := 0; i < 200; i++ {
				done = invocationCall(t, ctx, cs, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: args.RequestID}, false)
				if string(done["status"]) == `"completed"` {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if string(done["status"]) != `"completed"` || m.CodeExchanges.Load() != 1 || m.RefreshExchanges.Load() != 0 || m.Private.Runtime.Calls.Load() != 1 {
				t.Fatal("guided credential did not support one granted MCP invocation")
			}
			invocationCall(t, ctx, cs, "gregale_invoke_tool", args, false)
			if m.CodeExchanges.Load() != 1 || m.Private.Runtime.Calls.Load() != 1 {
				t.Fatal("receipt recovery repeated effects")
			}
			if _, err := f.Owner.RevokeToolOAuthConnection(ctx, m.Connection.ID); err != nil {
				t.Fatal(err)
			}
			publicationCall(t, ctx, cs, "gregale_get_tool_connection", toolConnectionArgs{GrantID: g.ID}, true)
		})
	}
}
