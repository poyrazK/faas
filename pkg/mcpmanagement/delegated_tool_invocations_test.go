package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestHostedDelegatedInvocationJourney(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f := granttest.New(t, state.NewMemStore(), testToken)
			app := granttest.NewRuntime(t, f)
			grantID := uuid.NewString()
			if _, err := f.Owner.PutToolGrant(t.Context(), grantID, api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			var workers atomic.Int32
			worker := func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
				workers.Add(1)
				if !slices.Equal(args[:3], []string{"mcp", "invocation-run", "--delegated"}) || slices.Contains(args, "--token-env") || slices.Contains(args, "--url") {
					t.Error("hosted worker accepted an override")
				}
				id := args[slices.Index(args, "--id")+1]
				_, err := mcphosting.RunRegisteredInvocation(ctx, mcphosting.RegisteredInvocationOptions{Delegated: true, Native: f.Agent, ID: id, Arguments: stdin, HTTP: app.Server.Client(), VerifyPin: func(context.Context, api.ToolInvocationTarget, string) error { return nil }})
				return LocalCLIResult{Stdout: []byte(`{"unbound":"not evidence"}`)}, err
			}
			opts := Options{APIBase: f.Native.URL, Environments: &EnvironmentConsent{}, AllowedApps: map[string]string{f.AppA.Slug: f.AppA.ID}, AllowToolInvocation: true, LocalCLI: worker, Toolsets: []string{ToolsetRegistry, ToolsetInvoke}}
			cs, ctx := connectSelectedTestServer(t, f.Native, opts, protocol)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) != 5 {
				t.Fatalf("catalog: %+v %v", catalog, err)
			}
			described := publicationCall(t, ctx, cs, "gregale_describe_tool", describeToolArgs{Version: f.PublicationA.ID, Name: "visible"}, false)
			if !strings.Contains(string(described["grants"]), grantID) {
				t.Fatal("agent cannot discover its exact invocation grant")
			}
			a := invokeToolArgs{GrantID: grantID, Version: f.PublicationA.ID, Name: "visible", RequestID: uuid.NewString(), ArgumentsJSON: `{"value":9007199254740993}`, AccessMode: "open", AllowToolExecution: true, TimeoutSeconds: 10}
			invocationCall(t, ctx, cs, "gregale_invoke_tool", a, false)
			var status map[string]json.RawMessage
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				status = invocationCall(t, ctx, cs, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: a.RequestID}, false)
				if string(status["status"]) == `"completed"` {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if string(status["status"]) != `"completed"` {
				t.Fatalf("status: %s", status["status"])
			}
			var encoded string
			_ = json.Unmarshal(status["result_base64"], &encoded)
			raw, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || !strings.Contains(string(raw), "9007199254740993") || strings.Contains(string(raw), testToken) || strings.Contains(string(raw), "never-retain") {
				t.Fatalf("unsafe result: %s", raw)
			}
			fresh, freshCtx := connectSelectedTestServer(t, f.Native, opts, protocol)
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", a, false)
			if workers.Load() != 1 || app.Calls.Load() != 1 {
				t.Fatal("new session repeated tool effects")
			}
			changed := a
			changed.ArgumentsJSON = `{"value":2}`
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", changed, true)
			changed = a
			changed.GrantID = uuid.NewString()
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", changed, true)
			changed = a
			changed.RequestID = uuid.NewString()
			changed.TokenEnv = "MCP_TOKEN"
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", changed, true)
			if _, err = f.Owner.RevokeToolGrant(t.Context(), grantID); err != nil {
				t.Fatal(err)
			}
			invocationCall(t, freshCtx, fresh, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: a.RequestID}, true)
		})
	}
}

func TestHostedInvocationConsentIndependentFromDeploy(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{AllowToolInvocation: true}}
	if !slices.Equal(r.scopes(), []string{scopeRead, scopeInvoke}) {
		t.Fatal("invocation enablement widened deployment scopes")
	}
	if got := grantScopes([]string{scopeRead, scopeInvoke, scopeWrite}, []string{api.ScopeAppsRead}); slices.Contains(got, scopeWrite) || !slices.Contains(got, scopeInvoke) {
		t.Fatal("native read-only key cannot retain invocation-only consent")
	}
	if _, ok := r.requestedScopes(scopeRead + " " + scopeInvoke); !ok {
		t.Fatal("invocation scope cannot be requested independently")
	}
	r.opts.AllowToolInvocation = false
	if _, ok := r.requestedScopes(scopeRead + " " + scopeInvoke); ok {
		t.Fatal("operator disabling invocation was bypassed")
	}
	page := consentPage(consentView{Account: "owner", Invocations: true, RequestID: "request", CSRF: "csrf", ConsentAction: "/oauth/consent"})
	if !strings.Contains(page, `name="allow_tool_invocation"`) || strings.Contains(page, `name="allow_writes"`) {
		t.Fatal("tool invocation consent requires deployment writes")
	}
}

func TestHostedInvocationOAuthRequiresExplicitConsent(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", StateStore: store, AllowToolInvocation: true, Toolsets: []string{ToolsetCore, ToolsetRegistry, ToolsetInvoke}, CLIRunner: func(string) LocalCLI {
		return func(context.Context, []string, []byte) (LocalCLIResult, error) { return LocalCLIResult{}, nil }
	}})
	f.mu.Lock()
	f.keyScopes = map[string][]string{"fp_test_alice_remote_key": {api.ScopeAppsRead}}
	f.mu.Unlock()
	flow := f.begin(scopeRead + " " + scopeInvoke)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}, f.server.URL)
	_ = start.Body.Close()
	if start.StatusCode != http.StatusOK {
		t.Fatalf("start sign-in: %d", start.StatusCode)
	}
	f.mu.Lock()
	mint := f.mints[len(f.mints)-1]
	f.mu.Unlock()
	if mint.Purpose != api.CliAuthPurposeMCPRead || slices.Contains(mint.Scopes, api.ScopeDeployWrite) {
		t.Fatal("invocation consent requested deployment authority")
	}
	review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
	if page := readPage(t, review); review.StatusCode != http.StatusOK || !strings.Contains(page, `name="allow_tool_invocation"`) || strings.Contains(page, `name="allow_writes"`) {
		t.Fatal("separate invocation consent missing")
	}
	denied := f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
	_ = denied.Body.Close()
	if denied.StatusCode != http.StatusBadRequest {
		t.Fatal("missing invocation checkbox approved")
	}
	approved := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"approve"}, "app_access": {"all"}, "allow_tool_invocation": {"yes"}, "toolsets": {"core,registry,invoke"}}, f.server.URL)
	_ = approved.Body.Close()
	if approved.StatusCode != http.StatusSeeOther {
		t.Fatalf("approval: %d", approved.StatusCode)
	}
	redirect, err := url.Parse(approved.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var token tokenResponse
	exchanged := f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
	decodeResponse(t, exchanged, &token)
	_ = exchanged.Body.Close()
	cs, ctx := f.connect(token, "2026-07-28")
	listed := listedTools(t, ctx, cs)
	if !listed["gregale_invoke_tool"] || !listed["gregale_get_tool_invocation"] || listed["gregale_apply_deploy"] || listed["gregale_plan_local_deploy"] {
		t.Fatal("invocation-only OAuth advertised incorrect authority")
	}
	restored, err := NewHTTPService(f.opts)
	if err != nil || len(restored.remote.grants) != 1 {
		t.Fatal("invocation consent was not retained across restart", err)
	}
	narrowed := f.opts
	narrowed.AllowToolInvocation = false
	disabled, err := NewHTTPService(narrowed)
	if err != nil || len(disabled.remote.grants) != 0 {
		t.Fatal("disabled operator policy restored invocation authority", err)
	}
}

func TestHostedInvocationLostCreationReplyDoesNotLaunchAgain(t *testing.T) {
	f := granttest.New(t, state.NewMemStore(), testToken)
	id := uuid.NewString()
	_, err := f.Owner.PutToolGrant(t.Context(), id, api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	f.LoseBegin = true
	var launches atomic.Int32
	opts := Options{APIBase: f.Native.URL, Environments: &EnvironmentConsent{}, AllowToolInvocation: true, Toolsets: []string{ToolsetInvoke}, LocalCLI: func(context.Context, []string, []byte) (LocalCLIResult, error) {
		launches.Add(1)
		return LocalCLIResult{}, nil
	}}
	cs, ctx := connectSelectedTestServer(t, f.Native, opts, "2026-07-28")
	in := invokeToolArgs{GrantID: id, Version: f.PublicationA.ID, Name: "visible", RequestID: uuid.NewString(), AccessMode: "open", AllowToolExecution: true, TimeoutSeconds: 10}
	lost := invocationCall(t, ctx, cs, "gregale_invoke_tool", in, true)
	if !strings.Contains(string(lost["recovery"]), "gregale_get_tool_invocation") {
		t.Fatal("lost reply did not preserve recovery identity")
	}
	fresh, freshCtx := connectSelectedTestServer(t, f.Native, opts, "2026-07-28")
	retained := invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", in, false)
	if string(retained["status"]) != `"queued"` || launches.Load() != 0 {
		t.Fatal("lost creation reply granted another worker launch")
	}
}

func TestHostedAgentKeyInvocationUsesIndependentOperatorOption(t *testing.T) {
	f := agentKeyFixture(t, map[string][]string{"fp_test_agent_reader": {api.ScopeAgent, api.ScopeAppsRead}})
	options := f.opts
	options.AllowToolInvocation = true
	options.AllowWrites = false
	options.Toolsets = []string{ToolsetRegistry, ToolsetInvoke}
	service, err := NewHTTPService(options)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	cs, ctx := f.connect(tokenResponse{AccessToken: "fp_test_agent_reader"}, "2026-07-28")
	listed := listedTools(t, ctx, cs)
	if len(listed) != 5 || !listed["gregale_invoke_tool"] || !listed["gregale_describe_tool"] {
		t.Fatal("apps-read agent key did not get grant-scoped invocation tools")
	}
}
