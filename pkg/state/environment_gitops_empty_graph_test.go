package state

import (
	"errors"
	"testing"
	"time"
)

func TestMemEnvironmentGitOpsEmptyGraphGuardKeepsServingWorkloadsPending(t *testing.T) {
	source := EnvironmentGitSource{ID: newID(), AccountID: newID(), ProjectID: newID(), EnvironmentID: newID(),
		EnvironmentSlug: "production", Generation: 1, ApprovedRevisionID: newID()}
	lease := EnvironmentGitOpsLease{Source: source, Revision: EnvironmentDesiredRevision{ID: source.ApprovedRevisionID},
		LeaseToken: newID(), LeaseUntil: time.Now().Add(time.Minute)}
	appID, deploymentID, releaseID := newID(), newID(), newID()
	store := &MemStore{
		projectEnvironments: map[string]ProjectEnvironment{source.EnvironmentID: {
			ID: source.EnvironmentID, AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: source.EnvironmentSlug}},
		environmentGitOps:        map[string]*environmentGitOpsMemory{source.ID: {source: source, lease: &lease}},
		activeProjectReleaseSets: map[string]string{releaseKey(source.ProjectID, source.EnvironmentSlug): releaseID},
		projectReleaseSets: map[string]ProjectReleaseSet{releaseID: {ID: releaseID, AccountID: source.AccountID,
			ProjectID: source.ProjectID, EnvironmentSlug: source.EnvironmentSlug, Active: true,
			Members: []ProjectReleaseMember{{AppID: appID, DeploymentID: deploymentID}}}},
		deployments: map[string]Deployment{deploymentID: {ID: deploymentID, AppID: appID, Scope: source.EnvironmentSlug,
			Status: DeployLive}},
		apps: map[string]App{appID: {ID: appID, AccountID: source.AccountID, ProjectID: source.ProjectID, Status: AppActive}},
	}
	managed, err := store.EnvironmentGitOpsHasServingManagedWorkload(t.Context(), lease)
	if err != nil || managed {
		t.Fatalf("unmanaged active release blocked configuration-only GitOps: managed=%t err=%v", managed, err)
	}
	deployment := store.deployments[deploymentID]
	deployment.EnvironmentWorkloadRuntime = `{}`
	deployment.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(false)
	store.deployments[deploymentID] = deployment
	managed, err = store.EnvironmentGitOpsHasServingManagedWorkload(t.Context(), lease)
	if err != nil || !managed {
		t.Fatalf("managed release was omitted from empty-graph guard: managed=%t err=%v", managed, err)
	}
	delete(store.activeProjectReleaseSets, releaseKey(source.ProjectID, source.EnvironmentSlug))
	managed, err = store.EnvironmentGitOpsHasServingManagedWorkload(t.Context(), lease)
	if err != nil || !managed {
		t.Fatalf("live-scope fallback bypassed empty-graph guard: managed=%t err=%v", managed, err)
	}
	deployment.EnvironmentWorkloadRuntime = ""
	store.deployments[deploymentID] = deployment
	managed, err = store.EnvironmentGitOpsHasServingManagedWorkload(t.Context(), lease)
	if err != nil || managed {
		t.Fatalf("inactive unmanaged release blocked configuration-only GitOps: managed=%t err=%v", managed, err)
	}
	store.environmentGitOps[source.ID].source.Generation++
	if _, err := store.EnvironmentGitOpsHasServingManagedWorkload(t.Context(), lease); !errors.Is(err, ErrConflict) {
		t.Fatalf("superseded lease bypassed empty-graph guard: %v", err)
	}
}
