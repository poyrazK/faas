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
	"github.com/onebox-faas/faas/pkg/state"
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
		"probe without contract": func(r *api.PutMCPListingRequest) { r.Contract, r.DropContract = nil, true },
		"contract and drop":      func(r *api.PutMCPListingRequest) { r.DropContract = true },
		"bad probe secret":       func(r *api.PutMCPListingRequest) { r.ProbeSecret = "1-token" },
		"probe secret with _":    func(r *api.PutMCPListingRequest) { r.ProbeSecret = "_TOKEN" },
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

// A republish without a contract keeps the one production deploys are
// checked against now, and its probe secret, unless it drops it.
func TestKeepContract(t *testing.T) {
	current := &state.MCPContractBaseline{Contract: contractJSON(t, "search", "fetch"), SHA256: "x", ProbeSecret: "MCP_PROBE_TOKEN"}
	listing, p := mcplisting.Validate(valid(), "acct", "app", hosts)
	if p != nil {
		t.Fatal(p)
	}
	kept, p := mcplisting.KeepContract(valid(), listing, current)
	if p != nil || len(kept.ContractSHA256) != 64 || kept.ProbeSecret != "MCP_PROBE_TOKEN" || len(kept.Contract) == 0 {
		t.Fatalf("kept = %+v, %v", kept, p)
	}
	drop := valid()
	drop.DropContract = true
	if dropped, p := mcplisting.KeepContract(drop, listing, current); p != nil || dropped.Contract != nil || dropped.ProbeSecret != "" {
		t.Fatalf("dropped = %+v, %v", dropped, p)
	}
	if plain, p := mcplisting.KeepContract(valid(), listing, nil); p != nil || plain.Contract != nil {
		t.Fatalf("first publish without contract = %+v, %v", plain, p)
	}
	probe := valid()
	probe.ProbeSecret = "OTHER_TOKEN"
	withProbe, _ := mcplisting.Validate(probe, "acct", "app", hosts)
	if renamed, p := mcplisting.KeepContract(probe, withProbe, current); p != nil || renamed.ProbeSecret != "OTHER_TOKEN" {
		t.Fatalf("renamed probe = %+v, %v", renamed, p)
	}
	if _, p := mcplisting.KeepContract(probe, withProbe, nil); p == nil {
		t.Fatal("probe secret without any contract accepted")
	}
	narrow := &state.MCPContractBaseline{Contract: contractJSON(t, "fetch")}
	if _, p := mcplisting.KeepContract(valid(), listing, narrow); p == nil {
		t.Fatal("kept contract missing a listed tool accepted")
	}
}
