package state_test

// adr: 20261128135329 — the tool safety scan in the store: the listing keeps
// its scan and review, the baseline carries whose high findings are
// reviewed, a check with unreviewed high findings needs a tool_scan
// override, and both override kinds are claimed together or not at all.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func checkMCPToolScanGate(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	gate := st.(state.MCPContractStore)
	listings := st.(state.MCPListingStore)
	scan := json.RawMessage(`{"rule_set":1,"findings":[{"rule":"instruction_override","severity":"high","kind":"tool","name":"search","path":"/description"}]}`)
	listing := state.MCPListing{AccountID: accountID, AppID: appID, Title: "Search", Endpoint: "https://search.example/mcp", Tools: []state.MCPListingTool{},
		Contract: json.RawMessage(`{"tools":[{"name":"search"}]}`), ContractSHA256: contractSHA('a'),
		ToolScan: scan, ToolScanHigh: 1, ToolScanReview: "quotes a user query", ToolScanReviewedBy: "user:" + accountID}
	if _, err := listings.PutMCPListing(ctx, listing); err != nil {
		t.Fatal(err)
	}
	stored, err := listings.GetMCPListing(ctx, accountID, appID)
	if err != nil || stored.ToolScanHigh != 1 || stored.ToolScanReview != "quotes a user query" || stored.ToolScanReviewedBy != "user:"+accountID ||
		stored.ToolScanAt == nil || len(stored.ToolScan) == 0 {
		t.Fatalf("stored listing scan = %+v, %v", stored, err)
	}
	base, err := gate.MCPContractBaseline(ctx, appID)
	if err != nil || len(base.ToolScan) == 0 || base.ToolScanAt == nil || !base.ToolScanReviewed {
		t.Fatalf("listing baseline scan = %+v, %v", base, err)
	}

	deploy := func() string {
		d, err := st.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:scan-" + uuid.NewString(), Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	override := func(kind, deploymentID string) state.MCPContractOverride {
		o, err := gate.CreateMCPContractOverride(ctx, state.MCPContractOverride{AppID: appID, AccountID: accountID, Kind: kind,
			Reason: kind + " reason", CreatedBy: "user:" + accountID, ExpiresAt: time.Now().Add(time.Hour), DeploymentID: deploymentID}, 5)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	if _, err := gate.CreateMCPContractOverride(ctx, state.MCPContractOverride{AppID: appID, AccountID: accountID, Kind: "bogus",
		Reason: "x", CreatedBy: "user:" + accountID, ExpiresAt: time.Now().Add(time.Hour)}, 5); err == nil {
		t.Fatal("override of an unknown kind was created")
	}

	// A breaking candidate with a new high finding: the contract override
	// alone admits nothing and stays open.
	newHigh := json.RawMessage(`{"rule_set":1,"findings":[{"rule":"concealment","severity":"high","kind":"tool","name":"web_search","path":"/description"}]}`)
	check := state.MCPContractCheck{DeploymentID: deploy(), AppID: appID, AccountID: accountID, BaselineSHA256: contractSHA('a'), CandidateSHA256: contractSHA('b'),
		Candidate: json.RawMessage(`{"tools":[{"name":"web_search"}]}`), Verdict: state.MCPContractBreaking,
		Changes:  json.RawMessage(`[{"tool":"search","path":"/tools/search","kind":"removed","severity":"breaking"}]`),
		ToolScan: newHigh, ToolScanHigh: 1, ToolScanNewHigh: 1}
	claims := state.MCPContractClaims{Contract: state.MCPContractClaimAny, ToolScan: state.MCPContractClaimAny}
	contractOverride := override(state.MCPContractOverrideKindContract, "")
	if _, err := gate.RecordMCPContractCheck(ctx, check, claims); !errors.Is(err, state.ErrMCPToolScanOverrideRequired) {
		t.Fatalf("new high finding without a tool_scan override = %v", err)
	}
	if open, _ := gate.ListOpenMCPContractOverrides(ctx, accountID, appID); len(open) != 1 || open[0].ID != contractOverride.ID {
		t.Fatalf("a refused check consumed the contract override: %+v", open)
	}

	// A tool_scan override alone does not admit the breaking change either,
	// and is not consumed.
	scanOverride := override(state.MCPContractOverrideKindToolScan, check.DeploymentID)
	contractOnlyMissing := check
	contractOnlyMissing.DeploymentID = deploy()
	if _, err := gate.RecordMCPContractCheck(ctx, contractOnlyMissing, state.MCPContractClaims{Contract: state.MCPContractClaimBound, ToolScan: state.MCPContractClaimAny}); !errors.Is(err, state.ErrMCPContractOverrideRequired) {
		t.Fatalf("bound contract claim with no bound override = %v", err)
	}

	recorded, err := gate.RecordMCPContractCheck(ctx, check, claims)
	if err != nil || recorded.OverrideID != contractOverride.ID || recorded.ToolScanOverrideID != scanOverride.ID || !recorded.ToolScanReviewed {
		t.Fatalf("check with both overrides = %+v, %v", recorded, err)
	}
	if open, _ := gate.ListOpenMCPContractOverrides(ctx, accountID, appID); len(open) != 0 {
		t.Fatalf("overrides left open after the check used them: %+v", open)
	}
	if err := st.MarkDeploymentLive(ctx, check.DeploymentID); err != nil {
		t.Fatal(err)
	}
	base, err = gate.MCPContractBaseline(ctx, appID)
	if err != nil || base.FromDeployment != check.DeploymentID || !base.ToolScanReviewed || string(base.ToolScan) == string(scan) {
		t.Fatalf("baseline after the overridden check went live = %+v, %v", base, err)
	}

	// The same reviewed finding on the next candidate is not new: it needs
	// no override and the check stays reviewed.
	carried := state.MCPContractCheck{DeploymentID: deploy(), AppID: appID, AccountID: accountID, BaselineSHA256: contractSHA('b'), CandidateSHA256: contractSHA('b'),
		Candidate: json.RawMessage(`{"tools":[{"name":"web_search"}]}`), Verdict: state.MCPContractUnchanged, Changes: json.RawMessage(`[]`),
		ToolScan: newHigh, ToolScanHigh: 1}
	if got, err := gate.RecordMCPContractCheck(ctx, carried, state.MCPContractClaims{}); err != nil || got.ToolScanOverrideID != "" || !got.ToolScanReviewed {
		t.Fatalf("carried finding = %+v, %v", got, err)
	}

	// An invalid check (more new high findings than high ones) is refused.
	bad := carried
	bad.DeploymentID, bad.ToolScanNewHigh = deploy(), 2
	if _, err := gate.RecordMCPContractCheck(ctx, bad, state.MCPContractClaims{ToolScan: state.MCPContractClaimAny}); err == nil {
		t.Fatal("check with new_high > high was recorded")
	}
}

// adr: 20261128135329
func TestMemStoreMCPToolScanGate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkMCPToolScanGate(t, ctx, store, accountID, appID)
}

// adr: 20261128135329
func TestPgStoreMCPToolScanGate(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkMCPToolScanGate(t, ctx, store, accountID, appID)
}
