package api

import "testing"

func TestMCPTransportVocabulary(t *testing.T) {
	for _, tc := range []struct {
		status  int
		outcome string
	}{{200, "http_success"}, {401, "unauthorized"}, {403, "forbidden"}, {400, "invalid_request"}, {504, "timeout"}, {429, "rate_limited"}, {499, "canceled"}, {502, "upstream_error"}, {404, "http_error"}, {0, "unknown"}} {
		if got := MCPHTTPOutcome(tc.status); got != tc.outcome {
			t.Fatalf("%d=%s", tc.status, got)
		}
	}
	if MCPMethodLabel("tools/call") != "tools/call" || MCPMethodLabel("arbitrary-private-name") != "other" {
		t.Fatal("unbounded RPC labels")
	}
}
