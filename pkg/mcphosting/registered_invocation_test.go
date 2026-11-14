package mcphosting_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolinvocation/invocationtest"
)

type invocationApp struct {
	fixture  *invocationtest.Fixture
	server   *httptest.Server
	calls    atomic.Int32
	requests atomic.Int32
	mode     atomic.Int32
	started  chan struct{}
	token    string
}

func newInvocationApp(t *testing.T, mode string) *invocationApp {
	t.Helper()
	f := invocationtest.New(t, state.NewMemStore(), mode)
	a := &invocationApp{fixture: f, started: make(chan struct{})}
	if mode == "external-oauth" {
		a.token = "application-reader-token"
	}
	a.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.requests.Add(1)
		if r.Header.Get("Authorization") == "Bearer "+f.NativeToken {
			t.Error("native credential was forwarded to the application")
		}
		if a.token != "" && r.Header.Get("Authorization") != "Bearer "+a.token {
			w.WriteHeader(401)
			return
		}
		if a.token == "" && r.Header.Get("Authorization") != "" {
			t.Error("open invocation unexpectedly carried a credential")
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Arguments map[string]json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := func(value any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": value})
		}
		switch request.Method {
		case "server/discover":
			result(map[string]any{"supportedVersions": []string{mcphosting.ProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}})
		case "tools/list":
			contract, err := mcphosting.ReadContract(strings.NewReader(string(f.Publication.Outcome.Catalog)))
			if err != nil {
				t.Error(err)
				return
			}
			if a.mode.Load() == 1 {
				contract.Tools[0].Description = "changed after publication"
			}
			result(map[string]any{"tools": contract.Tools})
		case "tools/call":
			if a.calls.Add(1) == 1 {
				close(a.started)
			}
			switch a.mode.Load() {
			case 2:
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			case 3:
				result(map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "known tool error"}}})
				return
			case 4:
				<-r.Context().Done()
				return
			}
			result(map[string]any{"content": []any{map[string]any{"type": "text", "text": f.NativeToken + " " + a.token}}, "structuredContent": map[string]any{"value": request.Params.Arguments["value"]}, "_meta": map[string]any{"private": "never-retain"}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(a.server.Close)
	u, _ := url.Parse(a.server.URL)
	f.PreviewURL, f.PreviewHost = a.server.URL, u.Host
	return a
}

func (a *invocationApp) options(t *testing.T, args []byte) mcphosting.RegisteredInvocationOptions {
	t.Helper()
	id := uuid.NewString()
	in := a.fixture.Intent(args)
	var tokenEnv string
	if a.token != "" {
		tokenEnv = "MCP_APPLICATION_TOKEN"
		in.CredentialBindingSHA256 = toolinvocation.CredentialBinding(tokenEnv)
	}
	if _, err := a.fixture.Client.BeginToolInvocation(t.Context(), id, in); err != nil {
		t.Fatal(err)
	}
	return mcphosting.RegisteredInvocationOptions{Native: a.fixture.Client, ID: id, Arguments: args, TokenEnv: tokenEnv, Token: a.token, HTTP: a.server.Client(), VerifyPin: func(context.Context, api.ToolInvocationTarget, string) error { return nil }}
}

func TestRegisteredInvocationExactVersionAndResult(t *testing.T) {
	for _, mode := range []string{"open", "external-oauth"} {
		t.Run(mode, func(t *testing.T) {
			a := newInvocationApp(t, mode)
			opts := a.options(t, []byte(`{"value":9007199254740993}`))
			record, err := mcphosting.RunRegisteredInvocation(t.Context(), opts)
			if err != nil || record.Status != "completed" || record.Outcome == nil {
				t.Fatalf("invocation: %+v %v", record, err)
			}
			result := string(record.Outcome.Result)
			if !strings.Contains(result, "9007199254740993") || strings.Contains(result, a.fixture.NativeToken) || a.token != "" && strings.Contains(result, a.token) || strings.Contains(result, "never-retain") {
				t.Fatalf("unsafe or imprecise result: %s", result)
			}
			if !record.Outcome.Redacted {
				t.Fatal("credential redaction not reported")
			}
			if again, err := mcphosting.RunRegisteredInvocation(t.Context(), opts); err != nil || again.Status != "completed" {
				t.Fatalf("receipt recovery: %v", err)
			}
			if a.calls.Load() != 1 {
				t.Fatal("receipt recovery repeated side effects")
			}
		})
	}
}

func TestRegisteredInvocationPreflightCannotDispatchDriftOrInvalidInput(t *testing.T) {
	for _, test := range []string{"catalog drift", "pin drift", "invalid input", "native token", "redirect"} {
		t.Run(test, func(t *testing.T) {
			a := newInvocationApp(t, "open")
			args := []byte(`{"value":1}`)
			if test == "invalid input" {
				args = []byte(`{"value":"not an integer"}`)
			}
			opts := a.options(t, args)
			switch test {
			case "catalog drift":
				a.mode.Store(1)
			case "pin drift":
				opts.VerifyPin = func(context.Context, api.ToolInvocationTarget, string) error { return errors.New("pin changed") }
			case "native token":
				opts.Token = a.fixture.NativeToken
			case "redirect":
				a.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "https://unrelated.example/mcp", 307)
				})
			}
			record, err := mcphosting.RunRegisteredInvocation(t.Context(), opts)
			if err != nil || record.Status != "not_started" || record.DispatchedAt != nil || a.calls.Load() != 0 {
				t.Fatalf("preflight dispatched: %+v %v", record, err)
			}
		})
	}
}

func TestRegisteredInvocationLostResponsesNeverReplay(t *testing.T) {
	for _, where := range []string{"tool response", "dispatch receipt", "tool error"} {
		t.Run(where, func(t *testing.T) {
			a := newInvocationApp(t, "open")
			opts := a.options(t, []byte(`{"value":1}`))
			switch where {
			case "tool response":
				a.mode.Store(2)
			case "dispatch receipt":
				a.fixture.LoseDispatch = true
			case "tool error":
				a.mode.Store(3)
			}
			record, err := mcphosting.RunRegisteredInvocation(t.Context(), opts)
			if where == "dispatch receipt" {
				if err == nil || a.calls.Load() != 0 {
					t.Fatal("lost grant receipt authorized execution")
				}
				record, err = a.fixture.Client.GetToolInvocation(t.Context(), opts.ID)
				if err != nil || record.Status != "running" {
					t.Fatal("lost dispatch fact not retained")
				}
			} else if err != nil || record.Status != map[string]string{"tool response": "outcome_unknown", "tool error": "tool_error"}[where] {
				t.Fatalf("outcome: %+v %v", record, err)
			}
			if _, err = mcphosting.RunRegisteredInvocation(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			want := int32(1)
			if where == "dispatch receipt" {
				want = 0
			}
			if a.calls.Load() != want {
				t.Fatal("uncertain or failed tool call replayed")
			}
		})
	}
}

func TestRegisteredInvocationCancellationIsNotRollback(t *testing.T) {
	a := newInvocationApp(t, "open")
	a.mode.Store(4)
	opts := a.options(t, []byte(`{"value":1}`))
	result := make(chan api.ToolInvocationRecord, 1)
	errCh := make(chan error, 1)
	go func() {
		record, err := mcphosting.RunRegisteredInvocation(t.Context(), opts)
		result <- record
		errCh <- err
	}()
	select {
	case <-a.started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never dispatched")
	}
	if _, err := a.fixture.Client.CancelToolInvocation(t.Context(), opts.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-result:
		if err := <-errCh; err != nil || record.Status != "outcome_unknown" || !record.CancellationRequested || record.DispatchedAt == nil {
			t.Fatalf("cancel claimed rollback: %+v %v", record, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop the local wait")
	}
	if a.calls.Load() != 1 {
		t.Fatal("cancel repeated a call")
	}
}
