package mcplisting_test

// adr: 20261127191645 — a listing's tool contract is stored canonical with
// its digest, covers every listed tool, and a probe secret needs it.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcplisting"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func contractJSON(t *testing.T, names ...string) json.RawMessage {
	t.Helper()
	tools := []mcphosting.Tool{}
	for _, n := range names {
		tools = append(tools, mcphosting.Tool{Name: n, InputSchema: map[string]any{"type": "object"}})
	}
	c, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mcphosting.MarshalContract(c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestValidateContract(t *testing.T) {
	req := valid()
	req.Contract = contractJSON(t, "search", "fetch")
	req.ProbeSecret = "MCP_PROBE_TOKEN"
	l, p := mcplisting.Validate(req, "acct", "app", hosts)
	if p != nil || len(l.ContractSHA256) != 64 || l.ProbeSecret != "MCP_PROBE_TOKEN" {
		t.Fatalf("listing with contract: %+v %v", l, p)
	}
	// Key order and whitespace do not change the digest.
	var generic map[string]any
	if err := json.Unmarshal(req.Contract, &generic); err != nil {
		t.Fatal(err)
	}
	indented, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	req.Contract = indented
	if again, p := mcplisting.Validate(req, "acct", "app", hosts); p != nil || again.ContractSHA256 != l.ContractSHA256 {
		t.Fatalf("re-encoded contract digest = %q, want %q (%v)", again.ContractSHA256, l.ContractSHA256, p)
	}
	if r := mcplisting.Response(l, "search", "https://gregale.dev"); r.ContractSHA256 != l.ContractSHA256 || r.ProbeSecret != "MCP_PROBE_TOKEN" {
		t.Fatalf("response = %+v", r)
	}
	if plain, p := mcplisting.Validate(valid(), "acct", "app", hosts); p != nil || plain.Contract != nil || plain.ContractSHA256 != "" {
		t.Fatalf("listing without contract: %+v %v", plain, p)
	}

	cases := map[string]func(*api.PutMCPListingRequest){
		"not a contract":         func(r *api.PutMCPListingRequest) { r.Contract = json.RawMessage(`{"tools":"x"}`) },
		"listed tool missing":    func(r *api.PutMCPListingRequest) { r.Contract = contractJSON(t, "fetch") },
		"probe without contract": func(r *api.PutMCPListingRequest) { r.Contract = nil },
		"bad probe secret":       func(r *api.PutMCPListingRequest) { r.ProbeSecret = "1-token" },
		"contract over the cap": func(r *api.PutMCPListingRequest) {
			r.Contract = json.RawMessage(`"` + strings.Repeat("x", api.MCPListingContractMaxBytes) + `"`)
		},
	}
	for name, mutate := range cases {
		req := valid()
		req.Contract = contractJSON(t, "search")
		req.ProbeSecret = "MCP_PROBE_TOKEN"
		mutate(&req)
		if _, p := mcplisting.Validate(req, "acct", "app", hosts); p == nil || p.Status != 422 {
			t.Errorf("%s: problem = %v, want 422", name, p)
		}
	}
}
