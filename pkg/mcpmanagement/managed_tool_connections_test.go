package mcpmanagement

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestHostedManagedToolConnection(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f := granttest.NewManaged(t, state.NewMemStore(), testToken)
			native := f.Private.Base
			opts := Options{APIBase: native.Native.URL, Token: testToken, AllowToolInvocation: true, AllowPrivateToolInvocation: true, Environments: &EnvironmentConsent{}, Toolsets: []string{ToolsetRegistry, ToolsetInvoke}}
			cs, ctx := connectSelectedTestServer(t, native.Native, opts, protocol)
			if !listedTools(t, ctx, cs)["gregale_get_tool_connection"] {
				t.Fatal("managed connection status is missing")
			}
			status := publicationCall(t, ctx, cs, "gregale_get_tool_connection", toolConnectionArgs{GrantID: f.Grant.ID}, false)
			if string(status["state"]) != `"refresh_required"` || f.Exchanges.Load() != 0 {
				t.Fatal("read-only status refreshed credentials")
			}
			args := invokeToolArgs{Version: native.PublicationA.ID, Name: "visible", GrantID: f.Grant.ID, CredentialBinding: f.Grant.Credential.SHA256, RequestID: uuid.NewString(), AccessMode: "external-oauth", ArgumentsJSON: `{"value":9007199254740993}`, AllowToolExecution: true, TimeoutSeconds: 15}
			invocationCall(t, ctx, cs, "gregale_invoke_tool", args, false)
			var done map[string]json.RawMessage
			for i := 0; i < 200; i++ {
				done = invocationCall(t, ctx, cs, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: args.RequestID}, false)
				if string(done["status"]) == `"completed"` {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if string(done["status"]) != `"completed"` || f.Exchanges.Load() != 1 || f.Private.Runtime.Calls.Load() != 1 {
				t.Fatal("hosted managed call did not complete once")
			}
			status = publicationCall(t, ctx, cs, "gregale_get_tool_connection", toolConnectionArgs{GrantID: f.Grant.ID}, false)
			raw, _ := json.Marshal(status)
			if string(status["state"]) != `"ready"` || strings.Contains(string(raw), "MCP_EXECUTION_REFRESH") || strings.Contains(string(raw), "provider-exec-") || strings.Contains(string(raw), "token_endpoint") {
				t.Fatal("connection projection leaked credentials or omitted readiness")
			}
			deniedOpts := opts
			deniedOpts.AllowPrivateToolInvocation = false
			denied, deniedCtx := connectSelectedTestServer(t, native.Native, deniedOpts, protocol)
			if listedTools(t, deniedCtx, denied)["gregale_get_tool_connection"] {
				t.Fatal("status bypassed independent private consent")
			}
			restrictedOpts := opts
			restrictedOpts.AllowedApps = map[string]string{native.AppB.Slug: native.AppB.ID}
			restricted, restrictedCtx := connectSelectedTestServer(t, native.Native, restrictedOpts, protocol)
			publicationCall(t, restrictedCtx, restricted, "gregale_get_tool_connection", toolConnectionArgs{GrantID: f.Grant.ID}, true)
			if _, err := native.Owner.RevokeToolOAuthConnection(ctx, f.Connection.ID); err != nil {
				t.Fatal(err)
			}
			publicationCall(t, ctx, cs, "gregale_get_tool_connection", toolConnectionArgs{GrantID: f.Grant.ID}, true)
			refused, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_invoke_tool", Arguments: args})
			if err != nil || !refused.IsError {
				t.Fatal("expected a structured authorization refusal", err)
			}
			if f.Exchanges.Load() != 1 || f.Private.Runtime.Calls.Load() != 1 {
				t.Fatal("revocation repeated effects")
			}
		})
	}
}
