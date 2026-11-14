package mcpmanagement

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestHostedPrivateToolInvocationJourney(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			p := granttest.NewPrivate(t, state.NewMemStore(), testToken)
			f := p.Base
			opts := Options{APIBase: f.Native.URL, Token: testToken, AllowToolInvocation: true, AllowPrivateToolInvocation: true, Environments: &EnvironmentConsent{}, Toolsets: []string{ToolsetRegistry, ToolsetInvoke}}
			cs, ctx := connectSelectedTestServer(t, f.Native, opts, protocol)
			tools := listedTools(t, ctx, cs)
			if !tools["gregale_invoke_tool"] || tools["gregale_apply_tool_deploy"] {
				t.Fatal("private invocation required local CLI or deployment authority")
			}
			desc := publicationCall(t, ctx, cs, "gregale_describe_tool", describeToolArgs{Version: f.PublicationA.ID, Name: "visible"}, false)
			raw, _ := json.Marshal(desc)
			if !strings.Contains(string(desc["grants"]), p.Grant.Credential.SHA256) || strings.Contains(string(raw), p.Token) || strings.Contains(string(raw), "MCP_TOOL_TOKEN") {
				t.Fatal("private grant projection omitted binding or leaked credentials")
			}
			args := invokeToolArgs{Version: f.PublicationA.ID, Name: "visible", GrantID: p.Grant.ID, CredentialBinding: p.Grant.Credential.SHA256, AccessMode: "external-oauth", RequestID: uuid.NewString(), ArgumentsJSON: `{"value":9007199254740993}`, AllowToolExecution: true, TimeoutSeconds: 10}
			invocationCall(t, ctx, cs, "gregale_invoke_tool", args, false)
			var done map[string]json.RawMessage
			for i := 0; i < 200; i++ {
				done = invocationCall(t, ctx, cs, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: args.RequestID}, false)
				if string(done["status"]) == `"completed"` {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if string(done["status"]) != `"completed"` {
				t.Fatalf("private invocation: %v", done)
			}
			var encoded string
			_ = json.Unmarshal(done["result_base64"], &encoded)
			result, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || strings.Contains(string(result), p.Token) || !strings.Contains(string(result), "9007199254740993") || p.Runtime.Calls.Load() != 1 {
				t.Fatalf("private result: %s %v", result, err)
			}
			fresh, freshCtx := connectSelectedTestServer(t, f.Native, opts, protocol)
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", args, false)
			changed := args
			changed.CredentialBinding = strings.Repeat("0", 64)
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", changed, true)
			deniedOpts := opts
			deniedOpts.AllowPrivateToolInvocation = false
			denied, deniedCtx := connectSelectedTestServer(t, f.Native, deniedOpts, protocol)
			invocationCall(t, deniedCtx, denied, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: args.RequestID}, true)
			resultDenied, callErr := denied.CallTool(deniedCtx, &mcp.CallToolParams{Name: "gregale_invoke_tool", Arguments: args})
			if callErr == nil && !resultDenied.IsError {
				t.Fatal("private invocation bypassed consent")
			}
			if p.Runtime.Calls.Load() != 1 {
				t.Fatal("private replay caused another call")
			}
			if _, err := f.Owner.RevokeToolGrant(ctx, p.Grant.ID); err != nil {
				t.Fatal(err)
			}
			invocationCall(t, freshCtx, fresh, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: args.RequestID}, true)
			args.RequestID = uuid.NewString()
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", args, true)
		})
	}
}

func TestHostedPrivateToolConsent(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{AllowToolInvocation: true, AllowPrivateToolInvocation: true}}
	if _, ok := r.requestedScopes(scopeRead + " " + scopePrivateInvoke); ok {
		t.Fatal("private scope omitted base invocation consent")
	}
	if _, ok := r.requestedScopes(scopeRead + " " + scopeInvoke + " " + scopePrivateInvoke); !ok {
		t.Fatal("private invocation required deployments")
	}
	if slices.Contains(r.scopes(), scopeWrite) {
		t.Fatal("private invocation granted deployments")
	}
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", StateStore: store, AllowToolInvocation: true, AllowPrivateToolInvocation: true, Toolsets: []string{ToolsetRegistry, ToolsetInvoke}})
	flow := f.begin(scopeRead + " " + scopeInvoke + " " + scopePrivateInvoke)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}, f.server.URL)
	_ = start.Body.Close()
	if start.StatusCode != http.StatusOK {
		t.Fatal("private login unavailable")
	}
	review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
	defer func() { _ = review.Body.Close() }()
	page := readPage(t, review)
	if !strings.Contains(page, `name="allow_private_tool_invocation"`) || strings.Contains(page, `name="allow_writes"`) {
		t.Fatal("private consent widened deployments")
	}
	approve := url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"approve"}, "app_access": {"all"}, "allow_tool_invocation": {"yes"}}
	denied := f.submitConsent(flow, approve, f.server.URL)
	_ = denied.Body.Close()
	if denied.StatusCode != http.StatusBadRequest {
		t.Fatal("private credential use was implicitly approved")
	}
	approve.Set("allow_private_tool_invocation", "yes")
	approved := f.submitConsent(flow, approve, f.server.URL)
	_ = approved.Body.Close()
	if approved.StatusCode != http.StatusSeeOther {
		t.Fatalf("private approval: %d", approved.StatusCode)
	}
	restored, err := NewHTTPService(f.opts)
	if err != nil || len(restored.remote.grants) != 1 {
		t.Fatal("private consent did not survive restart", err)
	}
	narrowed := f.opts
	narrowed.AllowPrivateToolInvocation = false
	disabled, err := NewHTTPService(narrowed)
	if err != nil || len(disabled.remote.grants) != 0 {
		t.Fatal("operator disable retained private consent", err)
	}
}

func TestHostedPrivateToolLostReceiptDoesNotRelaunch(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			p := granttest.NewPrivate(t, state.NewMemStore(), testToken)
			f := p.Base
			opts := Options{APIBase: f.Native.URL, Token: testToken, AllowToolInvocation: true, AllowPrivateToolInvocation: true, Environments: &EnvironmentConsent{}, Toolsets: []string{ToolsetRegistry, ToolsetInvoke}}
			cs, ctx := connectSelectedTestServer(t, f.Native, opts, protocol)
			args := invokeToolArgs{Version: f.PublicationA.ID, Name: "visible", GrantID: p.Grant.ID, CredentialBinding: p.Grant.Credential.SHA256, AccessMode: "external-oauth", RequestID: uuid.NewString(), ArgumentsJSON: `{}`, AllowToolExecution: true, TimeoutSeconds: 10}
			f.LosePrivateReply.Store(true)
			invocationCall(t, ctx, cs, "gregale_invoke_tool", args, true)
			fresh, freshCtx := connectSelectedTestServer(t, f.Native, opts, protocol)
			for i := 0; i < 200; i++ {
				status := invocationCall(t, freshCtx, fresh, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: args.RequestID}, false)
				if string(status["status"]) == `"completed"` {
					break
				}
				if i == 199 {
					t.Fatal("lost private receipt did not recover")
				}
				time.Sleep(10 * time.Millisecond)
			}
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", args, false)
			if p.Runtime.Calls.Load() != 1 {
				t.Fatal("lost private receipt caused another execution")
			}
		})
	}
}
