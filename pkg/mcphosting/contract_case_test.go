package mcphosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestRunContractCaseEnforcesReadOnlyToolsWhenAsked(t *testing.T) {
	calls := map[string]int{}
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		result := ""
		switch req.Method {
		case "initialize":
			result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}}}`, ProtocolVersion)
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "server/discover":
			result = fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{}}}`, ProtocolVersion)
		case "tools/list":
			result = `{"tools":[{"name":"search","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}},{"name":"refund","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			name, _ := req.Params["name"].(string)
			calls[name]++
			result = `{"content":[{"type":"text","text":"ok"}]}`
		default:
			w.WriteHeader(500)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	})
	expected := json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	ctx := context.Background()
	if err := RunContractCase(ctx, client, SmokeCase{Name: "s", Kind: "tool", Target: "search", ExpectedResult: expected}, true); err != nil {
		t.Fatalf("read-only tool: %v", err)
	}
	if err := RunContractCase(ctx, client, SmokeCase{Name: "r", Kind: "tool", Target: "refund", ExpectedResult: expected}, true); !errors.Is(err, ErrContractToolNotReadOnly) {
		t.Fatalf("mutating tool in production: %v", err)
	}
	if calls["refund"] != 0 {
		t.Fatal("non-read-only tool was called in production")
	}
	if err := RunContractCase(ctx, client, SmokeCase{Name: "r", Kind: "tool", Target: "refund", ExpectedResult: expected}, false); err != nil || calls["refund"] != 1 {
		t.Fatalf("non-production tool call: err=%v calls=%d", err, calls["refund"])
	}
	if err := RunContractCase(ctx, client, SmokeCase{Name: "m", Kind: "tool", Target: "missing", ExpectedResult: expected}, false); !errors.Is(err, ErrContractToolNotFound) {
		t.Fatalf("missing tool: %v", err)
	}
	if err := RunContractCase(ctx, client, SmokeCase{Name: "w", Kind: "tool", Target: "search", ExpectedResult: json.RawMessage(`{"other":1}`)}, true); !errors.Is(err, ErrContractCaseFailed) {
		t.Fatalf("wrong result: %v", err)
	}
}
