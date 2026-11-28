package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func contractSHA(c byte) string { return strings.Repeat(string(c), 64) }

// checkMCPContractGate walks the gate's store contract: the listing's
// contract is the baseline; a breaking check needs an override and consumes
// exactly one; a check is accepted only when its candidate goes live, which
// moves the baseline, fills the changelog and queues mcp.contract.changed.
func checkMCPContractGate(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	gate := st.(state.MCPContractStore)
	if _, err := gate.MCPContractBaseline(ctx, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("baseline without listing = %v, want ErrNotFound", err)
	}
	listing := state.MCPListing{AccountID: accountID, AppID: appID, Title: "Search", Endpoint: "https://search.example/mcp", Tools: []state.MCPListingTool{}}
	if _, err := st.(state.MCPListingStore).PutMCPListing(ctx, listing); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.MCPContractBaseline(ctx, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("baseline of a listing without contract = %v, want ErrNotFound", err)
	}
	listing.Contract, listing.ContractSHA256, listing.ProbeSecret = json.RawMessage(`{"tools":[{"name":"search"}]}`), contractSHA('a'), "MCP_PROBE_TOKEN"
	if _, err := st.(state.MCPListingStore).PutMCPListing(ctx, listing); err != nil {
		t.Fatal(err)
	}
	base, err := gate.MCPContractBaseline(ctx, appID)
	if err != nil || base.SHA256 != contractSHA('a') || base.FromDeployment != "" || base.ProbeSecret != "MCP_PROBE_TOKEN" || base.AccountID != accountID {
		t.Fatalf("listing baseline = %+v, %v", base, err)
	}
	hook, err := st.CreateAppWebhook(ctx, state.AppWebhook{AppID: appID, AccountID: accountID, TargetURL: "https://owner.example/hook",
		SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventMCPContractChanged)}})
	if err != nil {
		t.Fatal(err)
	}

	deploy := func() string {
		d, err := st.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:contract-" + uuid.NewString(), Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	breaking := state.MCPContractCheck{AppID: appID, AccountID: accountID, BaselineSHA256: contractSHA('a'), CandidateSHA256: contractSHA('b'),
		Candidate: json.RawMessage(`{"tools":[]}`), Verdict: state.MCPContractBreaking,
		Changes: json.RawMessage(`[{"tool":"search","path":"/tools/search","kind":"removed","severity":"breaking"}]`)}
	breaking.DeploymentID = deploy()
	if _, err := gate.RecordMCPContractCheck(ctx, breaking, true); !errors.Is(err, state.ErrMCPContractOverrideRequired) {
		t.Fatalf("breaking without override = %v", err)
	}
	override, err := gate.CreateMCPContractOverride(ctx, state.MCPContractOverride{AppID: appID, AccountID: accountID,
		Reason: "rename search", CreatedBy: "user:" + accountID, ExpiresAt: time.Now().Add(time.Hour)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.CreateMCPContractOverride(ctx, state.MCPContractOverride{AppID: appID, AccountID: accountID,
		Reason: "again", CreatedBy: "user:" + accountID, ExpiresAt: time.Now().Add(time.Hour)}, 1); !errors.Is(err, state.ErrMCPContractOverrideCap) {
		t.Fatalf("second open override = %v, want cap", err)
	}
	recorded, err := gate.RecordMCPContractCheck(ctx, breaking, true)
	if err != nil || recorded.OverrideID != override.ID || recorded.AcceptedAt != nil {
		t.Fatalf("breaking with override = %+v, %v", recorded, err)
	}
	// A redelivery reuses the override this deployment already consumed.
	if again, err := gate.RecordMCPContractCheck(ctx, breaking, true); err != nil || again.OverrideID != override.ID {
		t.Fatalf("rerecord = %+v, %v", again, err)
	}
	if open, err := gate.ListOpenMCPContractOverrides(ctx, accountID, appID); err != nil || len(open) != 0 {
		t.Fatalf("open overrides after use = %+v, %v", open, err)
	}
	if base, _ := gate.MCPContractBaseline(ctx, appID); base.SHA256 != contractSHA('a') {
		t.Fatalf("baseline moved before the candidate went live: %+v", base)
	}

	if err := st.MarkDeploymentLive(ctx, breaking.DeploymentID); err != nil {
		t.Fatal(err)
	}
	base, err = gate.MCPContractBaseline(ctx, appID)
	if err != nil || base.SHA256 != contractSHA('b') || base.FromDeployment != breaking.DeploymentID || string(base.Contract) != `{"tools": []}` && string(base.Contract) != `{"tools":[]}` {
		t.Fatalf("baseline after live = %+v, %v", base, err)
	}
	if _, err := gate.RecordMCPContractCheck(ctx, breaking, false); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("rewriting an accepted check = %v, want ErrConflict", err)
	}
	log, err := gate.ListMCPContractChangelog(ctx, accountID, appID, 20)
	if err != nil || len(log) != 1 || log[0].Verdict != state.MCPContractBreaking || log[0].OverrideID != override.ID {
		t.Fatalf("changelog = %+v, %v", log, err)
	}
	deliveries, _, err := st.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventMCPContractChanged {
		t.Fatalf("deliveries = %+v, %v", deliveries, err)
	}
	var payload struct {
		Verdict    string   `json:"verdict"`
		Tools      []string `json:"tools"`
		Overridden bool     `json:"overridden"`
	}
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil || payload.Verdict != "breaking" || len(payload.Tools) != 1 ||
		payload.Tools[0] != "search" || !payload.Overridden {
		t.Fatalf("payload = %s, %v", deliveries[0].Payload, err)
	}

	// An unchanged candidate is accepted silently; republishing resets the
	// baseline and the changelog.
	unchanged := state.MCPContractCheck{DeploymentID: deploy(), AppID: appID, AccountID: accountID, BaselineSHA256: contractSHA('b'),
		CandidateSHA256: contractSHA('b'), Candidate: json.RawMessage(`{"tools":[]}`), Verdict: state.MCPContractUnchanged}
	if _, err := gate.RecordMCPContractCheck(ctx, unchanged, false); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDeploymentLive(ctx, unchanged.DeploymentID); err != nil {
		t.Fatal(err)
	}
	if log, _ := gate.ListMCPContractChangelog(ctx, accountID, appID, 20); len(log) != 1 {
		t.Fatalf("unchanged check entered the changelog: %+v", log)
	}
	if deliveries, _, _ := st.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, ""); len(deliveries) != 1 {
		t.Fatalf("unchanged check queued a webhook: %+v", deliveries)
	}
	if checks, err := gate.ListMCPContractChecks(ctx, accountID, appID, 10); err != nil || len(checks) != 2 {
		t.Fatalf("checks = %+v, %v", checks, err)
	}
	time.Sleep(10 * time.Millisecond)
	listing.ContractSHA256 = contractSHA('c')
	if _, err := st.(state.MCPListingStore).PutMCPListing(ctx, listing); err != nil {
		t.Fatal(err)
	}
	if base, _ := gate.MCPContractBaseline(ctx, appID); base.SHA256 != contractSHA('c') || base.FromDeployment != "" {
		t.Fatalf("baseline after republish = %+v", base)
	}
	if log, _ := gate.ListMCPContractChangelog(ctx, accountID, appID, 20); len(log) != 0 {
		t.Fatalf("changelog after republish = %+v", log)
	}
	unverified := state.MCPContractCheck{DeploymentID: deploy(), AppID: appID, AccountID: accountID, BaselineSHA256: contractSHA('c'),
		Verdict: state.MCPContractUnverified, Candidate: json.RawMessage(`{}`)}
	if _, err := gate.RecordMCPContractCheck(ctx, unverified, false); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unverified with a candidate = %v, want ErrInvalidArgument", err)
	}
	checkMCPContractRefusalsAndRollback(t, ctx, st, deploy, accountID, appID)
}

// checkMCPContractRefusalsAndRollback: a refused check is recorded but never
// accepted; an override bound to one deployment admits only that one; a
// rollback that makes an older candidate live again moves the baseline back
// without a webhook. The baseline is contractSHA('c') on entry.
func checkMCPContractRefusalsAndRollback(t *testing.T, ctx context.Context, st state.Store, deploy func() string, accountID, appID string) {
	t.Helper()
	gate := st.(state.MCPContractStore)
	candidate := func(sha byte, verdict string) state.MCPContractCheck {
		return state.MCPContractCheck{DeploymentID: deploy(), AppID: appID, AccountID: accountID, BaselineSHA256: contractSHA('c'),
			CandidateSHA256: contractSHA(sha), Candidate: json.RawMessage(`{"tools":[]}`), Verdict: verdict,
			Changes: json.RawMessage(`[{"tool":"search","path":"/tools/search","kind":"tool_removed","severity":"breaking"}]`)}
	}
	refused := candidate('d', state.MCPContractBreaking)
	got, err := gate.RecordMCPContractCheck(ctx, refused, true)
	if !errors.Is(err, state.ErrMCPContractOverrideRequired) || got.DeploymentID != refused.DeploymentID || got.OverrideID != "" {
		t.Fatalf("refused check = %+v, %v", got, err)
	}
	if err := st.MarkDeploymentLive(ctx, refused.DeploymentID); err != nil {
		t.Fatal(err)
	}
	if base, _ := gate.MCPContractBaseline(ctx, appID); base.SHA256 != contractSHA('c') {
		t.Fatalf("a refused check moved the baseline: %+v", base)
	}
	if checks, _ := gate.ListMCPContractChecks(ctx, accountID, appID, 10); len(checks) == 0 || checks[0].DeploymentID != refused.DeploymentID || checks[0].AcceptedAt != nil {
		t.Fatalf("refused check not listed or accepted: %+v", checks)
	}

	bound := candidate('e', state.MCPContractBreaking)
	override, err := gate.CreateMCPContractOverride(ctx, state.MCPContractOverride{AppID: appID, AccountID: accountID, Reason: "bound",
		CreatedBy: "user:" + accountID, ExpiresAt: time.Now().Add(time.Hour), DeploymentID: bound.DeploymentID}, 5)
	if err != nil || override.DeploymentID != bound.DeploymentID {
		t.Fatalf("bound override = %+v, %v", override, err)
	}
	if _, err := gate.RecordMCPContractCheck(ctx, candidate('f', state.MCPContractBreaking), true); !errors.Is(err, state.ErrMCPContractOverrideRequired) {
		t.Fatalf("another deployment used the bound override: %v", err)
	}
	if got, err := gate.RecordMCPContractCheck(ctx, bound, true); err != nil || got.OverrideID != override.ID {
		t.Fatalf("bound deployment = %+v, %v", got, err)
	}
	if err := st.MarkDeploymentLive(ctx, bound.DeploymentID); err != nil {
		t.Fatal(err)
	}
	later := candidate('g', state.MCPContractCompatible)
	if _, err := gate.RecordMCPContractCheck(ctx, later, false); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDeploymentLive(ctx, later.DeploymentID); err != nil {
		t.Fatal(err)
	}
	if base, _ := gate.MCPContractBaseline(ctx, appID); base.SHA256 != contractSHA('g') {
		t.Fatalf("baseline before rollback = %+v", base)
	}
	log, _ := gate.ListMCPContractChangelog(ctx, accountID, appID, 20)
	time.Sleep(10 * time.Millisecond)
	// Roll back to the bound deployment.
	if err := st.UpdateDeploymentStatus(ctx, bound.DeploymentID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDeploymentLive(ctx, bound.DeploymentID); err != nil {
		t.Fatal(err)
	}
	if base, _ := gate.MCPContractBaseline(ctx, appID); base.SHA256 != contractSHA('e') || base.FromDeployment != bound.DeploymentID {
		t.Fatalf("baseline after rollback = %+v, want the rolled-back candidate", base)
	}
	if again, _ := gate.ListMCPContractChangelog(ctx, accountID, appID, 20); len(again) != len(log) || len(again) == 0 || !again[0].AcceptedAt.Equal(*log[0].AcceptedAt) {
		t.Fatalf("rollback changed the changelog: %+v, was %+v", again, log)
	}
}

// adr: 20261127191645
func TestMemStoreMCPContractGate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkMCPContractGate(t, ctx, store, accountID, appID)
}

// adr: 20261127191645
func TestPgStoreMCPContractGate(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkMCPContractGate(t, ctx, store, accountID, appID)
}
