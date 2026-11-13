package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolinvocation/invocationtest"
)

func invocationCall(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args any, wantError bool) map[string]json.RawMessage {
	t.Helper()
	out := callTool(t, ctx, cs, name, args, wantError)
	raw, _ := json.Marshal(out)
	validateWorkflowSchema(t, toolOutputSchema(name), raw)
	var result map[string]json.RawMessage
	_ = json.Unmarshal(raw, &result)
	return result
}

func TestRegisteredToolInvocationMCPJourney(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f := invocationtest.New(t, state.NewMemStore(), "open")
			f.NativeToken = testToken
			f.Client = api.NewClient(f.Native.URL, testToken)
			var workers, calls atomic.Int32
			cli := func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
				workers.Add(1)
				if len(args) < 4 || args[0] != "mcp" || args[1] != "invocation-run" || slices.Contains(args, "--url") || strings.Contains(strings.Join(args, " "), "9007199254740993") {
					t.Error("invocation used arbitrary URLs or argv arguments")
				}
				if string(stdin) != `{"value":9007199254740993}` {
					t.Error("argument bytes or numbers changed")
				}
				id := args[slices.Index(args, "--id")+1]
				grant, err := f.Client.DispatchToolInvocation(ctx, id)
				if err != nil {
					return LocalCLIResult{}, err
				}
				if grant.DispatchGranted {
					calls.Add(1)
					out, err := toolinvocation.Result([]byte(`{"content":[],"structuredContent":{"value":9007199254740993}}`))
					if err != nil {
						return LocalCLIResult{}, err
					}
					if _, err = f.Client.FinishToolInvocation(ctx, id, out); err != nil {
						return LocalCLIResult{}, err
					}
				}
				return LocalCLIResult{Stdout: []byte(`{"unbound":"CLI stdout is not evidence"}`)}, nil
			}
			opts := Options{APIBase: f.Native.URL, Token: f.NativeToken, AllowWrites: true, LocalCLI: cli, Toolsets: []string{ToolsetInvoke}}
			cs, ctx := connectSelectedTestServer(t, f.Native, opts, protocol)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) != 3 {
				t.Fatalf("invocation catalog: %v %v", catalog, err)
			}
			args := invokeToolArgs{Version: f.Publication.ID, Name: "echo", RequestID: uuid.NewString(), ArgumentsJSON: `{"value":9007199254740993}`, AccessMode: "open", AllowToolExecution: true, TimeoutSeconds: 10}
			invocationCall(t, ctx, cs, "gregale_invoke_tool", args, false)
			var status map[string]json.RawMessage
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				status = invocationCall(t, ctx, cs, "gregale_get_tool_invocation", toolInvocationArgs{InvocationID: args.RequestID}, false)
				if string(status["status"]) == `"completed"` {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if string(status["status"]) != `"completed"` || string(status["effects_may_have_occurred"]) != "true" {
				t.Fatalf("invocation: %s", status["status"])
			}
			var encoded string
			_ = json.Unmarshal(status["result_base64"], &encoded)
			result, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || !strings.Contains(string(result), "9007199254740993") {
				t.Fatal("MCP result numeric evidence changed")
			}
			// New management instance, no original connection memory or source files.
			fresh, freshCtx := connectSelectedTestServer(t, f.Native, opts, protocol)
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", args, false)
			changed := args
			changed.ArgumentsJSON = `{"value":2}`
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", changed, true)
			changed = args
			changed.AllowToolExecution = false
			invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", changed, true)
			invocationCall(t, freshCtx, fresh, "gregale_cancel_tool_invocation", toolInvocationArgs{InvocationID: args.RequestID}, false)
			if workers.Load() != 1 || calls.Load() != 1 {
				t.Fatal("receipt recovery or cancellation reexecuted a tool")
			}
		})
	}
}

func TestRegisteredToolInvocationLostStartReceipt(t *testing.T) {
	f := invocationtest.New(t, state.NewMemStore(), "open")
	f.NativeToken = testToken
	f.Client = api.NewClient(f.Native.URL, testToken)
	f.LoseBegin = true
	var calls atomic.Int32
	cli := func(context.Context, []string, []byte) (LocalCLIResult, error) {
		calls.Add(1)
		return LocalCLIResult{}, errors.New("should not start")
	}
	opts := Options{APIBase: f.Native.URL, Token: f.NativeToken, AllowWrites: true, LocalCLI: cli, Toolsets: []string{ToolsetInvoke}}
	cs, ctx := connectSelectedTestServer(t, f.Native, opts, "2026-07-28")
	args := invokeToolArgs{Version: f.Publication.ID, Name: "echo", RequestID: uuid.NewString(), ArgumentsJSON: `{"value":1}`, AccessMode: "open", AllowToolExecution: true, TimeoutSeconds: 10}
	err := invocationCall(t, ctx, cs, "gregale_invoke_tool", args, true)
	if !strings.Contains(string(err["recovery"]), "gregale_get_tool_invocation") || strings.Contains(string(err["recovery"]), "arguments_json") {
		t.Fatal("lost start recovery discarded the identity or echoed input")
	}
	fresh, freshCtx := connectSelectedTestServer(t, f.Native, opts, "2026-07-28")
	status := invocationCall(t, freshCtx, fresh, "gregale_invoke_tool", args, false)
	if string(status["status"]) != `"queued"` || calls.Load() != 0 {
		t.Fatal("lost start receipt authorized another worker")
	}
}

func TestRegisteredInvocationCatalogAuthority(t *testing.T) {
	for _, test := range []struct {
		name  string
		opts  Options
		count int
	}{
		{"read-only", Options{}, 1}, {"no executor", Options{AllowWrites: true}, 2},
		{"hosted", Options{AllowWrites: true, Environments: &EnvironmentConsent{}}, 0}, {"agent", Options{AllowWrites: true, agentKey: true}, 0},
		{"app", Options{AllowWrites: true, AllowedApps: map[string]string{"alpha": "app-one"}}, 0}, {"org", Options{AllowWrites: true, Org: "company"}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := invocationtest.New(t, state.NewMemStore(), "open")
			f.NativeToken = testToken
			f.Client = api.NewClient(f.Native.URL, testToken)
			test.opts.APIBase, test.opts.Token, test.opts.Toolsets = f.Native.URL, f.NativeToken, []string{ToolsetInvoke}
			cs, ctx := connectSelectedTestServer(t, f.Native, test.opts, "2026-07-28")
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) != test.count {
				t.Fatalf("authority catalog: %v %v", catalog, err)
			}
		})
	}
}
