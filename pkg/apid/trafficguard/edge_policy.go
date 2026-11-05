package trafficguard

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func EdgePolicyScope(raw string) (string, error) {
	switch raw {
	case "", "app":
		return "app", nil
	case "fleet", "account":
		return raw, nil
	default:
		return "", errors.New("edge_rules_scope must be app, account, or fleet")
	}
}

// Account revisions include other-app wildcard rules and unknown legacy owners.
// Gateway observations remain global replay watermarks, which cover every
// account mutation up to the same commit-ordered position.
func EdgePolicyPosition(ctx context.Context, store any, scope, appID, accountID string) (state.EdgeRulePolicyPosition, error) {
	switch scope {
	case "app":
		ledger, ok := store.(interface {
			LatestAppEdgeRuleChangeID(context.Context, string) (int64, error)
		})
		if !ok {
			return state.EdgeRulePolicyPosition{}, errors.New("app edge-rule status is unavailable")
		}
		revision, err := ledger.LatestAppEdgeRuleChangeID(ctx, appID)
		return state.EdgeRulePolicyPosition{DesiredRevision: revision}, err
	case "account":
		ledger, ok := store.(interface {
			LatestAccountEdgeRulePolicyPosition(context.Context, string) (state.EdgeRulePolicyPosition, error)
		})
		if !ok {
			return state.EdgeRulePolicyPosition{}, errors.New("account edge-rule status is unavailable")
		}
		return ledger.LatestAccountEdgeRulePolicyPosition(ctx, accountID)
	case "fleet":
		ledger, ok := store.(interface {
			LatestEdgeRuleChangeID(context.Context) (int64, error)
		})
		if !ok {
			return state.EdgeRulePolicyPosition{}, errors.New("fleet edge-rule status is unavailable")
		}
		revision, err := ledger.LatestEdgeRuleChangeID(ctx)
		return state.EdgeRulePolicyPosition{DesiredRevision: revision}, err
	default:
		return state.EdgeRulePolicyPosition{}, errors.New("invalid edge-rule scope")
	}
}

func SummarizeRuntimePolicyComponent(
	scope string,
	desired int64,
	gateways []state.ServingGatewayControlPlaneState,
	now time.Time,
	freshness time.Duration,
	revision func(state.ServingGatewayControlPlaneState) int64,
	observedAt func(state.ServingGatewayControlPlaneState) time.Time,
) api.RuntimePolicyComponentStatus {
	status := api.RuntimePolicyComponentStatus{
		Scope:           scope,
		DesiredRevision: desired,
		State:           "pending",
		ServingGateways: len(gateways),
	}
	if desired == 0 {
		status.State = "unverified"
		status.PendingGateways = len(gateways)
		return status
	}
	for _, gateway := range gateways {
		observed := observedAt(gateway)
		// Observations carry the database clock; compare against it when the
		// read reported one so apid clock skew cannot mark a gateway stale.
		databaseNow := now
		if !gateway.DatabaseNow.IsZero() {
			databaseNow = gateway.DatabaseNow
		}
		if observed.IsZero() || observed.Before(databaseNow.Add(-freshness)) || observed.After(databaseNow) {
			status.StaleGateways++
			status.PendingGateways++
			continue
		}
		if revision(gateway) < desired {
			status.PendingGateways++
			continue
		}
		status.AppliedGateways++
	}
	if len(gateways) == 0 {
		status.State = "unverified"
	} else if status.AppliedGateways == len(gateways) {
		status.State = "active"
	}
	return status
}

// EnrichMCPPolicyStatus adds one account-authorized snapshot without making
// legacy in-memory status stores manufacture gateway observations.
func EnrichMCPPolicyStatus(ctx context.Context, store any, accountID, appID string, status *api.RuntimePolicyStatusResponse) {
	if status.EdgeRules.Scope != "account" {
		return
	}
	source, ok := store.(state.MCPPolicyConvergenceStore)
	if !ok {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	defer cancel()
	snapshot, err := source.MCPPolicyConvergenceSnapshot(readCtx, accountID, appID)
	if err != nil {
		status.MCPConvergence = &api.MCPPolicyConvergenceSnapshot{State: "unknown"}
		return
	}
	status.MCPConvergence = &snapshot
}
