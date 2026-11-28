package mcpcontract_test

// adr: 20261127191645 — overrides carry a bounded reason and lifetime, and
// the public changelog shows only live changes and tool names.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpcontract"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidateOverride(t *testing.T) {
	reason, ttl, p := mcpcontract.ValidateOverride(api.CreateMCPContractOverrideRequest{Reason: "  rename search  "})
	if p != nil || reason != "rename search" || ttl != api.MCPContractOverrideDefaultTTL {
		t.Fatalf("default override = %q %v %v", reason, ttl, p)
	}
	if _, ttl, p := mcpcontract.ValidateOverride(api.CreateMCPContractOverrideRequest{Reason: "x", ExpiresInSeconds: 600}); p != nil || ttl != 10*time.Minute {
		t.Fatalf("ttl 600 = %v %v", ttl, p)
	}
	cases := map[string]api.CreateMCPContractOverrideRequest{
		"no reason":   {Reason: "  "},
		"long reason": {Reason: strings.Repeat("x", api.MCPContractReasonMax+1)},
		"bad utf8":    {Reason: "\xff"},
		"too short":   {Reason: "x", ExpiresInSeconds: 59},
		"too long":    {Reason: "x", ExpiresInSeconds: int(api.MCPContractOverrideMaxTTL.Seconds()) + 1},
		"negative":    {Reason: "x", ExpiresInSeconds: -1},
	}
	for name, req := range cases {
		if _, _, p := mcpcontract.ValidateOverride(req); p == nil || p.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s: problem = %v, want 422", name, p)
		}
	}
	_, _, p = mcpcontract.ValidateOverride(api.CreateMCPContractOverrideRequest{Reason: strings.Repeat("x", api.MCPContractReasonMax+1)})
	if p == nil || p.Limit == nil || p.Observed == nil {
		t.Fatalf("long reason problem lacks limit fields: %+v", p)
	}
}

func TestChangelog(t *testing.T) {
	live := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	changes, err := json.Marshal([]api.MCPContractChange{
		{Tool: "search", Path: "/tools/search/inputSchema", Kind: "required_added", Severity: "breaking"},
		{Tool: "search", Path: "/tools/search/description", Kind: "changed", Severity: "informational"},
		{Tool: "fetch", Path: "/tools/fetch", Kind: "added", Severity: "informational"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := mcpcontract.Changelog([]state.MCPContractCheck{
		{DeploymentID: "d2", Verdict: state.MCPContractBreaking, Changes: changes, AcceptedAt: &live},
		{DeploymentID: "d1", Verdict: state.MCPContractCompatible, Changes: changes},
	})
	if len(out) != 1 || !out[0].Date.Equal(live) || out[0].Verdict != state.MCPContractBreaking ||
		!slices.Equal(out[0].Tools, []string{"fetch", "search"}) || out[0].Changes != 3 {
		t.Fatalf("changelog = %+v", out)
	}
}
