package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgLifecycleGate(ctx context.Context, tx pgx.Tx, snapshot RoutePolicySnapshot, deployment Deployment, decision *api.RouteGateDecision, at time.Time, saved api.SavedRouteRequirements, fingerprint RouteCheckFingerprinter) error {
	baselines, err := (&sqlc.Queries{}).ReadLifecycleCanaryBaselines(ctx, tx, sqlc.ReadLifecycleCanaryBaselinesParams{AppID: snapshot.App.ID, CandidateID: deployment.ID, Scope: deployment.Scope})
	if err != nil {
		return err
	}
	candidate := snapshot.Contract
	// Check malformed candidate metadata even when this is the first serving revision.
	appendLifecycleGateFindings(decision, candidate, candidate, at, false)
	removal, err := pgRouteRemovalPolicy(ctx, tx, snapshot.Account.ID, snapshot.App.ID)
	if err != nil {
		return err
	}
	binding := lifecycleBinding(snapshot, api.CanaryRouteGate{Revision: decision.Revision}, saved, removal, fingerprint)
	for _, id := range baselines {
		baseline := RoutePolicySnapshot{Account: snapshot.Account, App: snapshot.App}
		if err := pgRoutePolicyContract(ctx, tx, &baseline, id, true); err != nil {
			return err
		}
		reviewed := false
		if baseline.Contract != nil && candidate != nil {
			receipts, err := sqlc.New().ReadValidRouteLifecycleApprovals(ctx, tx, sqlc.ReadValidRouteLifecycleApprovalsParams{AccountID: snapshot.Account.ID, AppID: snapshot.App.ID, BaselineID: id, CandidateID: deployment.ID, At: pgtype.Timestamptz{Time: at, Valid: true}, BaselineSha: baseline.Contract.SHA256, CandidateSha: candidate.SHA256, ConfigurationSha: binding.ConfigurationSHA256, GateRevision: binding.GateRevision, RequirementsRevision: binding.RequirementsRevision, RemovalRevision: binding.RemovalPolicyRevision})
			if err != nil {
				return err
			}
			for _, body := range receipts {
				var receipt api.RouteLifecycleApproval
				if err = json.Unmarshal(body, &receipt); err != nil {
					return err
				}
				if matchingLifecycleApproval(receipt, binding, baseline.Contract, candidate, at) {
					reviewed = true
					decision.LifecycleApprovalIDs = append(decision.LifecycleApprovalIDs, receipt.ID)
					break
				}
			}
		}
		appendLifecycleGateFindings(decision, baseline.Contract, candidate, at, reviewed)
	}
	return nil
}
