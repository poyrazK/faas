package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPCompleteCLIRequestsPromptAndResourceSuggestions(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	var seen int
	server := newMCPCompleteTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mcp-complete-token" {
			t.Errorf("wrong MCP token: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Mcp-Method") != "completion/complete" {
			t.Errorf("wrong MCP method header: %q", r.Header.Get("Mcp-Method"))
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Ref struct {
					Type string `json:"type"`
					Name string `json:"name"`
					URI  string `json:"uri"`
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
		if request.Method != "completion/complete" || request.Params.Argument.Name == "" {
			t.Errorf("unexpected completion request: %+v", request)
		}
		var values []string
		if request.Params.Ref.Type == "ref/prompt" {
			if request.Params.Ref.Name != "summarize" || request.Params.Argument.Name != "style" || request.Params.Argument.Value != "exec" || request.Params.Context.Arguments["period"] != "weekly" {
				t.Errorf("unexpected prompt completion parameters: %+v", request.Params)
			}
			values = []string{"executive"}
		} else if request.Params.Ref.Type == "ref/resource" {
			if request.Params.Ref.URI != "customer://records/{recordId}" || request.Params.Argument.Name != "recordId" || request.Params.Argument.Value != "" {
				t.Errorf("unexpected resource completion parameters: %+v", request.Params)
			}
			values = []string{"example-1", "example-2"}
		} else {
			t.Errorf("unknown completion reference: %+v", request.Params.Ref)
		}
		seen++
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"completion": map[string]any{"values": values, "total": len(values), "hasMore": false}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Setenv("MCP_COMPLETE_TEST_TOKEN", "mcp-complete-token")
	contextFile := filepath.Join(t.TempDir(), "context.json")
	if err := os.WriteFile(contextFile, []byte(`{"period":"weekly"}`), 0600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "prompt",
			args: []string{"complete", "--url", server.URL + "/mcp", "--token-env", "MCP_COMPLETE_TEST_TOKEN", "--prompt", "summarize", "--argument", "style", "--value", "exec", "--context-file", contextFile},
			want: []string{"executive"},
		},
		{
			name: "resource template with empty partial value",
			args: []string{"complete", "--url", server.URL + "/mcp", "--token-env", "MCP_COMPLETE_TEST_TOKEN", "--resource-template", "customer://records/{recordId}", "--argument", "recordId", "--value", ""},
			want: []string{"example-1", "example-2"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output.Reset()
			if code := cmdMCP(test.args); code != 0 {
				t.Fatalf("exit=%d output=%s", code, output.String())
			}
			var receipt struct {
				Completion mcphosting.CompletionSuggestions `json:"completion"`
			}
			if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(receipt.Completion.Values, test.want) {
				t.Fatalf("completion=%+v want=%v", receipt.Completion, test.want)
			}
		})
	}
	if seen != 2 {
		t.Fatalf("server received %d completion requests, want 2", seen)
	}
}

func TestMCPCompleteCLIRequiresOneReferenceAndValue(t *testing.T) {
	oldErr := osStderr
	var stderr bytes.Buffer
	osStderr = &stderr
	t.Cleanup(func() { osStderr = oldErr })
	for _, args := range [][]string{
		{"complete", "--url", "https://mcp.example/mcp", "--argument", "style", "--value", ""},
		{"complete", "--url", "https://mcp.example/mcp", "--prompt", "summarize", "--resource-template", "repo://{repo}", "--argument", "style", "--value", ""},
		{"complete", "--url", "https://mcp.example/mcp", "--prompt", "summarize", "--argument", "style"},
	} {
		stderr.Reset()
		if code := cmdMCP(args); code == 0 {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
}

func TestMCPCompleteCLIRejectsNonStringContext(t *testing.T) {
	oldErr := osStderr
	var stderr bytes.Buffer
	osStderr = &stderr
	t.Cleanup(func() { osStderr = oldErr })
	requests := 0
	server := newMCPCompleteTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"completion":{"values":[]}}}`))
	}))
	args := []string{"complete", "--url", server.URL + "/mcp", "--prompt", "summarize", "--argument", "style", "--value", "", "--context", `{"period":1}`}
	if code := cmdMCP(args); code == 0 {
		t.Fatal("non-string completion context was accepted")
	}
	if requests != 0 {
		t.Fatalf("invalid context made %d request(s)", requests)
	}
}

func newMCPCompleteTestServer(t *testing.T, handler http.Handler) *httptest.Server {
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
