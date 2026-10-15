package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/state"
)

func (b *environmentGitOpsBackend) reconcileWorkloadRetirements(ctx context.Context, lease state.EnvironmentGitOpsLease) (bool, error) {
	store, ok := b.intent.(state.EnvironmentGitOpsWorkloadRetirementStore)
	if !ok {
		return true, nil
	}
	records, err := store.ListEnvironmentGitOpsWorkloadRetirements(ctx, lease.Source.AccountID, lease.Source.ID)
	if err != nil {
		return false, err
	}
	needsWithdrawal := false
	for _, r := range records {
		if r.Phase == "retiring" && r.RouteDeploymentID != "" {
			needsWithdrawal = true
			break
		}
	}
	if !needsWithdrawal {
		return store.ReconcileEnvironmentGitOpsWorkloadRetirements(ctx, lease)
	}
	nodes, err := b.server.edgeRules.ServingEdgeRuleNodes(ctx)
	if err != nil {
		return false, err
	}
	return environmentgitops.ReconcileWorkloadRetirements(ctx, store, b.server.notif, lease, nodes, edgerules.EdgeRuleConvergenceTimeout, edgerules.EdgeRuleNotificationRetry)
}

func (s *server) environmentGitOpsWorkloadRetirements(ctx context.Context, accountID, sourceID string) ([]api.EnvironmentWorkloadRetirement, error) {
	store, ok := s.store.(state.EnvironmentGitOpsWorkloadRetirementStore)
	if !ok {
		return nil, nil
	}
	return store.ListEnvironmentGitOpsWorkloadRetirements(ctx, accountID, sourceID)
}
