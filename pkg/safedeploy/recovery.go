package safedeploy

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type exactCanaryRecoveryClient interface {
	RecoverDeploymentRolloutAndIdempotencyKey(context.Context, string, string, string, string, string) (api.RolloutTransitionResponse, error)
}

type recoveryPredecessorReader interface {
	LiveDeployments(context.Context, string) ([]state.Deployment, error)
}

// Production clients pin the failing deployment and its sole serving predecessor.
// Once exact recovery is supported, an unavailable/ambiguous recipient must not
// fall back to app-selected recovery. Legacy clients retain off-policy behavior.
func recoverExactCanaryAbort(ctx context.Context, client any, targets RolloutTargetResolver, candidate state.Deployment, reason, key string) (bool, error) {
	recovery, ok := client.(exactCanaryRecoveryClient)
	if !ok {
		return false, nil
	}
	reader, ok := targets.(recoveryPredecessorReader)
	if !ok {
		return true, fmt.Errorf("%w: predecessor reader unavailable", ErrActionTargetUnavailable)
	}
	rows, err := reader.LiveDeployments(ctx, candidate.AppID)
	if err != nil {
		return true, fmt.Errorf("%w: read predecessor: %w", ErrActionTargetUnavailable, err)
	}
	normalScope := func(scope string) string {
		if scope == "" {
			return "default"
		}
		return scope
	}
	var predecessor state.Deployment
	for _, d := range rows {
		if d.ID == candidate.ID || d.AppID != candidate.AppID || d.Status != state.DeployLive || d.TrafficPercent <= 0 || normalScope(d.Scope) != normalScope(candidate.Scope) {
			continue
		}
		if predecessor.ID != "" {
			return true, fmt.Errorf("%w: multiple serving predecessors", ErrActionTargetAmbiguous)
		}
		if !d.CreatedAt.Before(candidate.CreatedAt) || isActiveCanary(d) {
			return true, fmt.Errorf("%w: invalid serving predecessor", ErrActionTargetUnavailable)
		}
		predecessor = d
	}
	if predecessor.ID == "" {
		return true, fmt.Errorf("%w: no serving predecessor", ErrActionTargetUnavailable)
	}
	_, err = recovery.RecoverDeploymentRolloutAndIdempotencyKey(ctx, candidate.ID, predecessor.ID, "abort", reason, key+"/"+predecessor.ID)
	return true, err
}
