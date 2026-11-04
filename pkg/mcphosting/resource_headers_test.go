package mcphosting

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestResourceReadCarriesModernRoutingHeader(t *testing.T) {
	for _, version := range []string{ProtocolVersion, LegacyProtocolVersion} {
		for _, tc := range []struct{ uri, header string }{
			{"greeting://welcome", "greeting://welcome"},
			{"records://users/é", "=?base64?cmVjb3JkczovL3VzZXJzL8Op?="},
		} {
			t.Run(version+"/"+tc.uri, func(t *testing.T) {
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					var request struct {
						ID     int            `json:"id"`
						Method string         `json:"method"`
						Params map[string]any `json:"params"`
					}
					_ = json.NewDecoder(r.Body).Decode(&request)
					want := ""
					if version == ProtocolVersion {
						want = tc.header
					}
					if got := r.Header.Get("Mcp-Name"); got != want {
						t.Errorf("Mcp-Name=%q want=%q", got, want)
					}
					if request.Method != "resources/read" || request.Params["uri"] != tc.uri {
						t.Errorf("unexpected request: %+v", request)
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"contents": []any{}}})
				})
				c.Version = version
				if _, err := c.ReadResource(context.Background(), tc.uri); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
