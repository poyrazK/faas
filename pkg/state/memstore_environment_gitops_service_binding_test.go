package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreEnvironmentGitOpsServiceBindingUsesExactActiveRelease(t *testing.T) {
	store, caller, callerDeployment, target, targetDeployment, releaseID := environmentGitOpsServiceBindingFixture(t, true)
	route, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(context.Background(), caller.ID, callerDeployment.ID, "database")
	if err != nil || !managed || !found {
		t.Fatalf("ResolveEnvironmentGitOpsServiceBinding() = (%+v, %v, %v, %v)", route, managed, found, err)
	}
	if route.CallerAppID != caller.ID || route.CallerDeploymentID != callerDeployment.ID || route.TargetAppID != target.ID ||
		route.TargetDeploymentID != targetDeployment.ID || route.ReleaseSetID != releaseID || route.AccountID != caller.AccountID || route.RequireHTTPS {
		t.Fatalf("route = %+v", route)
	}
	if route.CallScope != nil || route.Reliability != nil {
		t.Fatalf("unexpected route policy: %+v", route)
	}
}

func TestMemStoreEnvironmentGitOpsServiceBindingDoesNotFallBackWhenUnbound(t *testing.T) {
	store, caller, callerDeployment, _, _, _ := environmentGitOpsServiceBindingFixture(t, false)
	route, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(context.Background(), caller.ID, callerDeployment.ID, "database")
	if err != nil || !managed || found || route != (EnvironmentGitOpsServiceBindingRoute{}) {
		t.Fatalf("unbound route = (%+v, %v, %v, %v), want managed miss without error", route, managed, found, err)
	}
}

func TestMemStoreEnvironmentGitOpsServiceBindingAcceptsResolvedWorkerAndJobIdentities(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  string
		class WorkloadClass
		want  bool
	}{
		{name: "worker app/deployment identity", mode: api.ExecutionModeWorker, class: WorkloadClassWorker, want: true},
		{name: "scheduled job identity resolved from its live claimed task", mode: api.ExecutionModeJob, class: WorkloadClassJob, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, caller, deployment, target, targetDeployment, releaseID := environmentGitOpsServiceBindingFixture(t, true)
			frozen, err := deployment.ScopedWorkloadRuntime()
			if err != nil || frozen == nil {
				t.Fatalf("read caller runtime: %+v %v", frozen, err)
			}
			frozen.WorkloadClass = tc.class
			frozen.Baseline.ExecutionMode = tc.mode
			raw, err := json.Marshal(frozen)
			if err != nil {
				t.Fatal(err)
			}
			deployment.EnvironmentWorkloadRuntime = string(raw)
			store.deployments[deployment.ID] = deployment

			route, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(context.Background(), caller.ID, deployment.ID, "database")
			if tc.want {
				if err != nil || !managed || !found || route.CallerAppID != caller.ID || route.CallerDeploymentID != deployment.ID ||
					route.TargetAppID != target.ID || route.TargetDeploymentID != targetDeployment.ID || route.ReleaseSetID != releaseID {
					t.Fatalf("resolved caller service route = (%+v, %v, %v, %v)", route, managed, found, err)
				}
				return
			}
			if !managed || found || !errors.Is(err, ErrConflict) {
				t.Fatalf("caller service route = (%+v, %v, %v, %v), want fail-closed conflict", route, managed, found, err)
			}
		})
	}
}

func TestMemStoreEnvironmentGitOpsServiceBindingRequiresActiveTargetMember(t *testing.T) {
	store, caller, callerDeployment, _, _, _ := environmentGitOpsServiceBindingFixture(t, true)
	store.mu.Lock()
	releaseID := store.activeProjectReleaseSets[releaseKey(caller.ProjectID, callerDeployment.Scope)]
	release := store.projectReleaseSets[releaseID]
	release.Members = release.Members[:1]
	store.projectReleaseSets[releaseID] = release
	store.mu.Unlock()

	_, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(context.Background(), caller.ID, callerDeployment.ID, "database")
	if !managed || found || !errors.Is(err, ErrConflict) {
		t.Fatalf("stale target route = (managed=%v, found=%v, err=%v), want fail-closed conflict", managed, found, err)
	}
}

func environmentGitOpsServiceBindingFixture(t *testing.T, includeBinding bool) (*MemStore, App, Deployment, App, Deployment, string) {
	t.Helper()
	store := NewMemStore()
	store.environmentGitOps = map[string]*environmentGitOpsMemory{}
	store.projectReleaseSets = map[string]ProjectReleaseSet{}
	store.activeProjectReleaseSets = map[string]string{}
	accountID, projectID, environmentID, sourceID := newID(), newID(), newID(), newID()
	callerID, callerDeploymentID, targetID, targetDeploymentID, releaseID := newID(), newID(), newID(), newID(), newID()
	caller := App{ID: callerID, AccountID: accountID, ProjectID: projectID, Slug: "git-api", Status: AppActive}
	target := App{ID: targetID, AccountID: accountID, ProjectID: projectID, Slug: "git-database", Status: AppActive}
	store.apps[caller.ID], store.apps[target.ID] = caller, target

	base := EnvironmentWorkloadRuntime{
		Source:  &api.EnvironmentWorkloadSource{Kind: "image", Image: "ghcr.io/example/workload@sha256:" + strings.Repeat("c", 64)},
		Runtime: map[string]json.RawMessage{}, SourceID: sourceID, EnvironmentID: environmentID, RevisionID: newID(),
		DefinitionDigest: strings.Repeat("b", 64), Generation: 1, IntentVersion: 1,
		PlanHash: strings.Repeat("a", 64), AppType: AppTypeApp, RuntimeBase: "node22", WorkloadClass: WorkloadClassHTTP,
		Scope: "production",
	}
	callerFrozen := base
	callerFrozen.AppID, callerFrozen.Resource = caller.ID, "workload/api"
	callerFrozen.ServiceBindings = map[string]EnvironmentScopedServiceBinding{}
	if includeBinding {
		callerFrozen.ServiceBindings["database"] = EnvironmentScopedServiceBinding{Workload: "database", EnvKey: "DATABASE_URL", TargetAppID: target.ID}
	}
	targetFrozen := base
	targetFrozen.AppID, targetFrozen.Resource = target.ID, "workload/database"
	callerDeployment := environmentGitOpsServiceBindingDeployment(t, callerDeploymentID, caller.ID, callerFrozen)
	targetDeployment := environmentGitOpsServiceBindingDeployment(t, targetDeploymentID, target.ID, targetFrozen)
	store.deployments[callerDeployment.ID], store.deployments[targetDeployment.ID] = callerDeployment, targetDeployment

	source := EnvironmentGitSource{ID: sourceID, AccountID: accountID, ProjectID: projectID, EnvironmentID: environmentID,
		EnvironmentSlug: "production", Spec: api.EnvironmentGitSourceSpec{Mode: "enforce"}}
	store.environmentGitOps[sourceID] = &environmentGitOpsMemory{source: source}
	release := ProjectReleaseSet{ID: releaseID, AccountID: accountID, ProjectID: projectID, EnvironmentSlug: "production", Active: true,
		Members: []ProjectReleaseMember{{AppID: caller.ID, DeploymentID: callerDeployment.ID}, {AppID: target.ID, DeploymentID: targetDeployment.ID}}}
	store.projectReleaseSets[release.ID] = release
	store.activeProjectReleaseSets[releaseKey(projectID, "production")] = release.ID
	return store, caller, callerDeployment, target, targetDeployment, releaseID
}

func environmentGitOpsServiceBindingDeployment(t *testing.T, id, appID string, frozen EnvironmentWorkloadRuntime) Deployment {
	t.Helper()
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	held := environmentWorkloadHeldFlag(false)
	return Deployment{ID: id, AppID: appID, Scope: "production", Status: DeployLive, Kind: DeploymentKindImage,
		ImageDigest: frozen.Source.Image, EnvironmentWorkloadRuntime: string(raw), EnvironmentWorkloadHeldValue: held}
}
