package promotion

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/apid/durable"
	"github.com/onebox-faas/faas/pkg/state"
)

func FallbackMatches(ctx context.Context, store state.Store, appsBySlug map[string]state.App, environment string, workloads []state.ProjectEnvironmentPromotionWorkload) bool {
	expected := make(map[string]string, len(workloads))
	for _, workload := range workloads {
		app, ok := appsBySlug[workload.WorkloadSlug]
		if !ok {
			return false
		}
		if workload.PreviousTargetTrafficPercent == 100 && workload.PreviousTargetDeploymentID != "" {
			expected[app.ID] = workload.PreviousTargetDeploymentID
		}
	}
	for _, app := range appsBySlug {
		deployments, err := store.LiveDeployments(ctx, app.ID)
		if err != nil {
			return false
		}
		var weighted []state.Deployment
		for _, deployment := range deployments {
			if deployment.Scope == environment && deployment.Status == state.DeployLive && deployment.TrafficPercent > 0 {
				weighted = append(weighted, deployment)
			}
		}
		want := expected[app.ID]
		if want == "" {
			if len(weighted) != 0 {
				return false
			}
		} else if len(weighted) != 1 || weighted[0].ID != want || weighted[0].TrafficPercent != 100 {
			return false
		}
	}
	return true
}

func RollbackWorkload(ctx context.Context, store state.Store, promotion state.ProjectEnvironmentPromotion, workload state.ProjectEnvironmentPromotionWorkload, appID string) (string, string, error) {
	deployments, currentErr := store.LiveDeployments(ctx, appID)
	if currentErr != nil {
		return "", "", fmt.Errorf("could not inspect current target deployments: %w", currentErr)
	}
	var current state.Deployment
	for _, deployment := range deployments {
		if deployment.Scope != promotion.ToEnvironment || deployment.TrafficPercent <= 0 {
			continue
		}
		if current.ID != "" || deployment.TrafficPercent != 100 {
			return "", "", fmt.Errorf("%w: target traffic is split; refusing to overwrite it", state.ErrConflict)
		}
		current = deployment
	}
	hasCurrent := current.ID != ""
	previousID := workload.PreviousTargetDeploymentID
	marker := "environment promotion/" + promotion.ID

	var candidate state.Deployment
	if workload.TargetDeploymentID != "" && workload.TargetDeploymentID != previousID {
		candidate, currentErr = store.DeploymentByID(ctx, workload.TargetDeploymentID)
		if currentErr != nil {
			return "", "", fmt.Errorf("could not load promotion deployment: %w", currentErr)
		}
		if candidate.AppID != appID || candidate.Scope != promotion.ToEnvironment || candidate.Reason != marker {
			return "", "", fmt.Errorf("%w: recorded deployment is not owned by this promotion", state.ErrConflict)
		}
	} else if hasCurrent && current.Reason == marker {
		candidate = current
	}

	if previousID != "" {
		previous, err := store.DeploymentByID(ctx, previousID)
		if err != nil {
			return "", "", fmt.Errorf("could not load previous target deployment: %w", err)
		}
		if previous.AppID != appID || previous.Scope != promotion.ToEnvironment || previous.DeletedAt != nil {
			return "", "", fmt.Errorf("%w: previous target deployment is not a restorable target", state.ErrConflict)
		}
		if hasCurrent && current.ID == previous.ID {
			if current.TrafficPercent != 100 || state.IsServiceRollout(current) {
				return "", "", fmt.Errorf("%w: previous target is not the sole serving release", state.ErrConflict)
			}
			return "restored", previous.ID, nil
		}
		if candidate.ID == "" || !hasCurrent || current.ID != candidate.ID || candidate.Status != state.DeployLive {
			return "", "", fmt.Errorf("%w: target changed after promotion; refusing to overwrite it", state.ErrConflict)
		}
		if err := durable.TransferPromotionValidator(ctx, previous, previous.ID); err != nil {
			return "", "", err
		}
		restore, ok := store.(state.RollbackTrafficStore)
		if !ok {
			return "", "", fmt.Errorf("guarded project rollback is unavailable")
		}
		if err := restore.RestoreDeploymentTrafficIfCurrent(ctx, appID, previous.ID, candidate.ID); err != nil {
			if errors.Is(err, state.ErrCheckedRollbackChanged) {
				return "", "", fmt.Errorf("%w: target changed after promotion", state.ErrConflict)
			}
			return "", "", fmt.Errorf("could not restore previous target deployment: %w", err)
		}
		return "restored", previous.ID, nil
	}

	if !hasCurrent && candidate.Status != state.DeployLive {
		return "cleared", "", nil
	}
	if !hasCurrent {
		for _, deployment := range deployments {
			if deployment.Scope == promotion.ToEnvironment && deployment.ID != candidate.ID {
				// Failing the candidate can rebalance traffic onto another
				// dark revision. Leave an operator's staged release alone.
				return "", "", fmt.Errorf("%w: target changed after promotion; refusing to clear it", state.ErrConflict)
			}
		}
	}
	if candidate.ID == "" || hasCurrent && current.ID != candidate.ID || candidate.Status != state.DeployLive {
		return "", "", fmt.Errorf("%w: target changed after promotion; refusing to clear it", state.ErrConflict)
	}
	if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeployFailed, "environment promotion rollback"); err != nil {
		return "", "", fmt.Errorf("could not clear promotion deployment: %w", err)
	}
	return "cleared", "", nil
}
