package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type RouteHealthRecoveryResult struct {
	Deployment Deployment
	Aborted    bool
	Decision   *api.RouteHealthDecision
	AuditID    int64
}
type RouteHealthRecoveryStore interface {
	RecoverCanaryRouteHealth(context.Context, string, string, string, int) (RouteHealthRecoveryResult, error)
}

func validateRouteHealthRecoveryCandidate(d Deployment, expectedStep int) error {
	if expectedStep < 0 || d.Status != DeployLive || d.CanaryTotalSteps <= 0 || d.CanaryStep >= d.CanaryTotalSteps ||
		d.TrafficPercent <= 0 || (NormalizeRolloutState(d.RolloutState) != "pending" && NormalizeRolloutState(d.RolloutState) != "rolling_out") {
		return ErrCanaryStateInvalid
	}
	if d.CanaryStep != expectedStep {
		return ErrCanaryStepConflict
	}
	return nil
}

func routeHealthRecoveryAudit(accountID string, entry api.RouteHealthHistoryEntry) (DeploymentAudit, error) {
	dep, err := uuid.Parse(entry.Report.DeploymentID)
	if err != nil {
		return DeploymentAudit{}, fmt.Errorf("parse route health recovery deployment: %w", err)
	}
	account, err := uuid.Parse(accountID)
	if err != nil {
		return DeploymentAudit{}, fmt.Errorf("parse route health recovery account: %w", err)
	}
	data, err := json.Marshal(map[string]any{"action": "abort", "reason": "critical_route_server_error_regression",
		"from_percent": entry.TrafficPercent, "to_percent": 0, "stable_deployment_id": entry.Report.StableDeploymentID, "route_health": entry.Decision})
	if err != nil {
		return DeploymentAudit{}, fmt.Errorf("encode route health recovery audit: %w", err)
	}
	return DeploymentAudit{DeploymentID: dep, AccountID: &account, Kind: DeployRolledBack,
		Actor: "meterd:route_health_recovery", At: entry.CheckedAt, Data: data}, nil
}
