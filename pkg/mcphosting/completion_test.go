package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestCompletePromptSendsContextAndDecodesSuggestions(t *testing.T) {
	server := newCompletionTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Method") != "completion/complete" || r.Header.Get("Authorization") != "Bearer completion-token" {
			t.Errorf("completion headers were not sent: method=%q authorization=%q", r.Header.Get("Mcp-Method"), r.Header.Get("Authorization"))
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Ref struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"ref"`
				Argument struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"argument"`
				Context struct {
					Arguments map[string]string `json:"arguments"`
				} `json:"context"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Method != "completion/complete" || request.Params.Ref.Type != "ref/prompt" || request.Params.Ref.Name != "summarize" || request.Params.Argument.Name != "style" || request.Params.Argument.Value != "exec" || request.Params.Context.Arguments["period"] != "weekly" {
			t.Errorf("unexpected completion request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"completion":{"values":["executive"],"total":1,"hasMore":false}}}`, request.ID)
	}))

	client, err := NewClient(server.URL, "completion-token", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), CompletionReference{Type: "ref/prompt", Name: "summarize"}, "style", "exec", map[string]string{"period": "weekly"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Values) != 1 || result.Values[0] != "executive" || result.Total == nil || *result.Total != 1 || result.HasMore == nil || *result.HasMore {
		t.Fatalf("unexpected completion suggestions: %+v", result)
	}
}

func TestCompleteResourceTemplateUsesResourceReference(t *testing.T) {
	server := newCompletionTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Error(err)
			return
		}
		ref, _ := params["ref"].(map[string]any)
		if request.Method != "completion/complete" || ref["type"] != "ref/resource" || ref["uri"] != "customer://records/{recordId}" {
			t.Errorf("unexpected resource completion request: %s", request.Params)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"completion":{"values":["example-1","example-2"]}}}`, request.ID)
	}))
	client, err := NewClient(server.URL, "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), CompletionReference{Type: "ref/resource", URI: "customer://records/{recordId}"}, "recordId", "example-", nil)
	if err != nil || len(result.Values) != 2 {
		t.Fatalf("completion=%+v err=%v", result, err)
	}
}

func TestCompleteRejectsInvalidInputsAndOversizedResults(t *testing.T) {
	requests := 0
	server := newCompletionTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		values := make([]string, maxCompletionSuggestions+1)
		for i := range values {
			values[i] = "suggestion"
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"completion": map[string]any{"values": values}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	client, err := NewClient(server.URL, "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), CompletionReference{Type: "ref/prompt", Name: "summarize", URI: "unexpected"}, "style", "", nil); err == nil {
		t.Fatal("invalid completion reference was accepted")
	}
	if requests != 0 {
		t.Fatalf("invalid input sent %d request(s)", requests)
	}
	if _, err := client.Complete(context.Background(), CompletionReference{Type: "ref/prompt", Name: "summarize"}, "style", "", nil); err == nil || !strings.Contains(err.Error(), "more than 100") {
		t.Fatalf("oversized completion result error=%v", err)
	}
}

func TestDiscoverCapturesCompletionsCapability(t *testing.T) {
	server := newCompletionTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"completions":{}}}}`, request.ID, ProtocolVersion)
	}))
	client, err := NewClient(server.URL, "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, _, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Completions == nil || !slices.Contains(capabilityNames(capabilities), "completions") {
		t.Fatalf("completion capability was not captured: %+v", capabilities)
	}
}

func newCompletionTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	t.Cleanup(server.Close)
	return server
}
