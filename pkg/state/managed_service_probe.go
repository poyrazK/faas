package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

type ManagedServiceProbeStore interface {
	EnvironmentGitOpsServiceBindingStore
	ServiceBindingRevisionStore
	AppByID(context.Context, string) (App, error)
	DeploymentByID(context.Context, string) (Deployment, error)
}

// ResolveManagedServiceProbe observes the current active graph and policies.
// Repeat before dispatch and evidence reuse: a snapshot grants no authority.
func ResolveManagedServiceProbe(ctx context.Context, store ManagedServiceProbeStore, appID, deploymentID, service string) (api.ManagedServiceProbeSpec, error) {
	var zero api.ManagedServiceProbeSpec
	app, err := store.AppByID(ctx, appID)
	if err != nil {
		return zero, err
	}
	dep, err := store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return zero, err
	}
	caller, err := managedProbeRuntime(app, dep)
	if err != nil {
		return zero, err
	}
	route, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(ctx, appID, deploymentID, service)
	if err != nil {
		return zero, err
	}
	if !managed || !found || route.CallerAppID != appID || route.CallerDeploymentID != deploymentID || route.AccountID != app.AccountID {
		return zero, ErrConflict
	}
	targetApp, err := store.AppByID(ctx, route.TargetAppID)
	if err != nil {
		return zero, err
	}
	targetDep, err := store.DeploymentByID(ctx, route.TargetDeploymentID)
	if err != nil {
		return zero, err
	}
	target, err := managedProbeRuntime(targetApp, targetDep)
	if err != nil || target.WorkloadClass != WorkloadClassHTTP || app.ID == targetApp.ID || app.AccountID != targetApp.AccountID || app.ProjectID != targetApp.ProjectID || dep.Scope != targetDep.Scope || target.Resource != "workload/"+service || caller.SourceID != target.SourceID || caller.EnvironmentID != target.EnvironmentID || caller.RevisionID != target.RevisionID || caller.Generation != target.Generation || caller.IntentVersion != target.IntentVersion || caller.PlanHash != target.PlanHash || caller.DefinitionDigest != target.DefinitionDigest {
		return zero, ErrConflict
	}
	binding := ""
	for _, edge := range caller.ServiceBindings {
		if edge.Workload == service && edge.TargetAppID == targetApp.ID {
			if binding != "" {
				return zero, ErrConflict
			}
			binding = edge.EnvKey
		}
	}
	policy, err := store.ReadServiceBindingRevision(ctx, app.AccountID, app.ID)
	if err != nil {
		return zero, err
	}
	spec := api.ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: dep.ID, Scope: normalizedDeploymentScope(dep.Scope), Service: service, Binding: binding,
		TargetAppID: targetApp.ID, TargetDeploymentID: targetDep.ID, ReleaseID: route.ReleaseSetID}
	if !route.RequireHTTPS {
		// ADR-20261019200059. An HTTPS spec keeps the empty transport, so its
		// context revision and existing receipts are unchanged.
		spec.Transport = api.ServiceBindingTransportHTTP
	}
	callerArtifact, err := bindingVerificationArtifact(dep)
	if err != nil {
		return zero, err
	}
	targetArtifact, err := bindingVerificationArtifact(targetDep)
	if err != nil {
		return zero, err
	}
	raw, _ := json.Marshal(struct {
		Spec           api.ManagedServiceProbeSpec
		Caller, Target *EnvironmentWorkloadRuntime
		Policy         string
		Artifacts      [2]string
	}{spec, caller, target, policy, [2]string{callerArtifact, targetArtifact}})
	digest := sha256.Sum256(raw)
	spec.ContextRevision = hex.EncodeToString(digest[:])
	if !spec.Valid() {
		return zero, ErrConflict
	}
	return spec, nil
}

// managedProbeRuntime admits HTTP and worker callers with a consistent frozen
// class/mode pair. ResolveManagedServiceProbe applies the stricter HTTP-only
// target rule separately: allowing a worker caller never exposes a worker port.
func managedProbeRuntime(app App, dep Deployment) (*EnvironmentWorkloadRuntime, error) {
	if app.Status == AppDeleted || app.PreviewOfSlug != "" || dep.AppID != app.ID || dep.Status != DeployLive || !dep.EnvironmentWorkloadManaged() || dep.EnvironmentWorkloadHeld() {
		return nil, ErrConflict
	}
	if _, err := bindingVerificationArtifact(dep); err != nil {
		return nil, err
	}
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil {
		return nil, ErrConflict
	}
	manifest, err := EffectiveEnvironmentWorkloadManifest(*frozen)
	if err != nil {
		return nil, ErrConflict
	}
	mode := manifest.ExecutionMode
	if mode == "" {
		mode = api.ExecutionModeRequest
	}
	switch frozen.WorkloadClass {
	case WorkloadClassHTTP:
		if mode != api.ExecutionModeRequest && mode != api.ExecutionModeService {
			return nil, ErrConflict
		}
	case WorkloadClassWorker:
		if mode != api.ExecutionModeWorker {
			return nil, ErrConflict
		}
	default:
		return nil, ErrConflict
	}
	return frozen, nil
}

func ManagedServiceProbeCurrent(ctx context.Context, store ManagedServiceProbeStore, task AppTask) error {
	if task.BindingVerification == nil || task.BindingVerification.ManagedService == nil {
		return nil
	}
	want := task.BindingVerification.ManagedService
	got, err := ResolveManagedServiceProbe(ctx, store, task.AppID, task.DeploymentID, want.Service)
	if err != nil || got != *want {
		return ErrConflict
	}
	return nil
}

// AppTaskGuestCommand keeps the durable target-probe format compatible while
// giving old probe-aware guests an unsupported reserved argument shape.
func AppTaskGuestCommand(task AppTask) []string {
	if task.BindingVerification != nil && task.BindingVerification.ManagedService != nil {
		return task.BindingVerification.ManagedService.Command()
	}
	return append([]string(nil), task.Command...)
}

func (m *MemStore) managedProbeAdmissionCurrentLocked(app App, dep Deployment, spec api.ManagedServiceProbeSpec) bool {
	release := m.projectReleaseSets[spec.ReleaseID]
	if !release.Active || m.activeProjectReleaseSets[releaseKey(app.ProjectID, dep.Scope)] != release.ID || release.AccountID != app.AccountID || releaseMemberForApp(release, app.ID) != dep.ID || releaseMemberForApp(release, spec.TargetAppID) != spec.TargetDeploymentID {
		return false
	}
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil || dep.EnvironmentWorkloadHeld() {
		return false
	}
	source := m.environmentGitOps[frozen.SourceID]
	return source != nil && !source.source.Detached && !source.source.Suspended && source.source.Spec.Mode == "enforce"
}
