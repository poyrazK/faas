package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// Gateway replay attests invalidation, not token validity or JWKS availability.
// The final fingerprint read binds readiness to the audit; the transaction
// guard continues to fence later stored-policy changes.
func waitMCPAccessConvergence(ctx context.Context, client *Client, slug, appID, resource, fingerprint string, revisionOut *int64) mcphosting.Check {
	check := mcphosting.Check{Name: "gateway_policy_convergence", Status: "unknown", Detail: "Could not verify fresh gateway observations of the audited policy; traffic promotion is blocked."}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		status, err := client.GetAppsSlugAccountEdgePolicyStatus(ctx, slug)
		if err != nil || status.AppID != appID || status.EdgeRules.Scope != "account" {
			return check
		}
		edge := status.EdgeRules
		if edge.State == "active" && edge.DesiredRevision > 0 && edge.ServingGateways > 0 && edge.AppliedGateways == edge.ServingGateways && edge.PendingGateways == 0 && edge.StaleGateways == 0 {
			rules, err := client.ListEdgeRules(ctx)
			if err != nil {
				return check
			}
			current, err := api.MCPAccessPolicyFingerprint(resource, rules, time.Now())
			if err != nil {
				return check
			}
			if current != fingerprint {
				check.Status = "failed"
				check.Detail = "Stored access policy changed during gateway verification; audit again before promotion."
				return check
			}
			if revisionOut != nil {
				*revisionOut = edge.DesiredRevision
			}
			check.Status = "passed"
			check.Detail = "Every registered serving gateway freshly observed the account edge-rule revision; stored policy still matches the audit."
			return check
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return check
		case <-timer.C:
		}
	}
}
