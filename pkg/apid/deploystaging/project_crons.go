package deploystaging

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type ProjectCronPlan struct {
	Request api.DeploymentCronStagingRequest
	Changed bool
	Reuse   *state.Deployment
}

// PlanProjectCrons replaces only HTTP schedules. Stable identities and operator
// options survive declarations that still name the same schedule and path.
func PlanProjectCrons(ctx context.Context, store state.DeploymentCronStagingStore, accountID, appID string, desired []api.DeploymentCronConfig) (ProjectCronPlan, error) {
	snapshot, err := store.DeploymentCronStagingSnapshot(ctx, accountID, appID)
	if err != nil {
		return ProjectCronPlan{}, err
	}
	next := make([]api.DeploymentCronConfig, 0, len(desired))
	for _, want := range desired {
		want.ID, want.Timezone = uuid.NewString(), "UTC"
		for _, old := range snapshot.Crons {
			if old.Schedule == want.Schedule && old.Path == want.Path {
				want.ID, want.Timezone, want.SkipIfRunning = old.ID, old.Timezone, old.SkipIfRunning
				break
			}
		}
		next = append(next, want)
	}
	plan := ProjectCronPlan{Request: api.DeploymentCronStagingRequest{Operation: "prepare", SessionID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedAppID: appID, ExpectedRevision: snapshot.Revision, ExpectedDeploymentID: snapshot.LatestDeploymentID, Before: snapshot.Crons, Desired: next}, Changed: !api.DeploymentCronsEqual(snapshot.Crons, next)}
	pendingStore, ok := store.(state.PendingDeploymentCronStore)
	if !ok {
		return plan, nil
	}
	pending, err := pendingStore.PendingDeploymentCrons(ctx, accountID, appID)
	if err != nil || pending == nil {
		return plan, err
	}
	if pending.Revision != snapshot.Revision || !api.DeploymentCronsEqual(pending.Intent.Before, snapshot.Crons) {
		return plan, nil
	}
	// Compare declarations without freshly allocated IDs for new schedules.
	equal := projectCronDeclarationsEqual(next, pending.Intent.Desired)
	if !equal {
		plan.Changed = true
		return plan, nil
	}
	if configStore, ok := store.(state.DeploymentConfigurationStore); ok {
		config, err := configStore.DeploymentConfiguration(ctx, accountID, pending.Deployment.ID)
		if err != nil {
			return plan, err
		}
		if config.Crons.Status == "pending" && config.Crons.SessionID == pending.Intent.SessionID {
			plan.Reuse = &pending.Deployment
		}
	}
	return plan, nil
}
func projectCronDeclarationsEqual(a, b []api.DeploymentCronConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if x.Schedule == y.Schedule && x.Path == y.Path && x.Enabled == y.Enabled && x.Timezone == y.Timezone && x.SkipIfRunning == y.SkipIfRunning {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
