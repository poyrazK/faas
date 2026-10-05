package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// Reservation publishes identities and scoped build intent only. It cannot
// create a deployment, launch a VM, or establish serving authority.
type EnvironmentGitOpsWorkloadCreationStore interface {
	PrepareEnvironmentGitOpsWorkloads(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) ([]EnvironmentGitOpsStep, error)
}

func newEnvironmentWorkloadApp(source EnvironmentGitSource, name string, workload api.EnvironmentWorkload, plan api.Plan) (App, error) {
	limits, ok := api.LimitsFor(plan)
	if !ok || workload.App != "" || workload.Source == nil {
		return App{}, ErrEnvironmentWorkloadPreparationUnavailable
	}
	// The environment UUID survives source rebinding. The logical name and UUID
	// define the slug; a collision never grants adoption of an existing app.
	hash := sha256.Sum256([]byte(source.EnvironmentID + "#" + name))
	app := App{AccountID: source.AccountID, ProjectID: source.ProjectID,
		Slug: "git-" + hex.EncodeToString(hash[:8]) + "-" + name[:min(len(name), 40)],
		Type: AppTypeApp, Status: AppActive, WorkloadClass: WorkloadClassHTTP,
		RAMMB: limits.RAMMB, IdleTimeoutS: limits.IdleTimeoutS, MaxConcurrency: 1,
		Visibility: api.AppVisibilityInternal}
	app.RootDir, app.WorkloadName = ".", app.Slug
	var runtime api.AppManifest
	if len(workload.Runtime) != 0 && json.Unmarshal(workload.Runtime, &runtime) != nil {
		return App{}, ErrInvalidArgument
	}
	if runtime.ValidateLifecyclePlan(plan) != nil {
		return App{}, ErrInvalidArgument
	}
	switch runtime.EffectiveExecutionMode() {
	case api.ExecutionModeWorker:
		app.WorkloadClass, app.Manifest.ExecutionMode = WorkloadClassWorker, api.ExecutionModeWorker
	case api.ExecutionModeJob:
		return App{}, ErrEnvironmentWorkloadPreparationUnavailable // separate job execution adapter
	}
	if workload.Source.Kind == "function" {
		if app.WorkloadClass != WorkloadClassHTTP || !api.ValidFunctionRuntime(workload.Source.Runtime) {
			return App{}, ErrInvalidArgument
		}
		app.Type, app.Runtime = AppTypeFunction, workload.Source.Runtime
	}
	return app, nil
}

func missingEnvironmentWorkloads(source EnvironmentGitSource, desired environmentsync.DesiredState, snapshot gitOpsIntentSnapshot, plan environmentsync.Plan) (map[string]App, error) {
	if source.Spec.Mode != "enforce" || !plan.CanApply() {
		return nil, ErrConflict
	}
	mapped := map[string]bool{}
	for _, resource := range snapshot.Resources {
		mapped[resource.Resource] = true
	}
	apps := map[string]App{}
	for _, change := range plan.Changes {
		if change.Path != "presence" || change.Action != "create" || !strings.HasPrefix(change.Resource, "workload/") {
			continue
		}
		// Lost mapped identities require an explicit recovery decision.
		if mapped[change.Resource] {
			return nil, ErrConflict
		}
		name := strings.TrimPrefix(change.Resource, "workload/")
		app, err := newEnvironmentWorkloadApp(source, name, desired.Definition.Workloads[name], snapshot.Plan)
		if err != nil {
			return nil, err
		}
		apps[change.Resource] = app
	}
	return apps, nil
}

func environmentWorkloadCreationChanges(plan environmentsync.Plan, created map[string]App) []environmentsync.Change {
	var changes []environmentsync.Change
	for _, change := range plan.Changes {
		if _, exists := created[change.Resource]; exists && (change.Path == "presence" || gitOpsWorkloadField(change.Path)) {
			changes = append(changes, change)
		}
	}
	return changes
}

func environmentWorkloadCreationNames(apps map[string]App) []string {
	return slices.Sorted(maps.Keys(apps))
}
