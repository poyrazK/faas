package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestEnvironmentGitOpsActivationBuildsWholeProjectReleaseWithFallback(t *testing.T) {
	projectID, accountID := uuid.NewString(), uuid.NewString()
	apiAppID, workerAppID := uuid.NewString(), uuid.NewString()
	oldAPIDeployment, candidateDeployment, workerDeployment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &MemStore{
		apps: map[string]App{
			apiAppID:    {ID: apiAppID, AccountID: accountID, ProjectID: projectID, Status: AppActive, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}},
			workerAppID: {ID: workerAppID, AccountID: accountID, ProjectID: projectID, Status: AppActive, Manifest: AppManifest{RevisionPinTTLSeconds: 900}},
		},
		deployments: map[string]Deployment{
			oldAPIDeployment: {ID: oldAPIDeployment, AppID: apiAppID, Scope: "production", Status: DeployLive, TrafficPercent: 100},
			candidateDeployment: {ID: candidateDeployment, AppID: apiAppID, Scope: "production", Status: DeploySnapshotting,
				EnvironmentWorkloadRuntime: `{}`, EnvironmentWorkloadHeldValue: environmentWorkloadHeldFlag(true)},
			workerDeployment: {ID: workerDeployment, AppID: workerAppID, Scope: "production", Status: DeployLive, TrafficPercent: 100},
		},
	}
	source := EnvironmentGitSource{AccountID: accountID, ProjectID: projectID, EnvironmentSlug: "production"}
	graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/api", AppID: apiAppID, CandidateDeploymentID: candidateDeployment,
	}}}

	members, fallback, ttl, err := store.environmentGitOpsReleaseMembersLocked(source, graph, "")
	if err != nil {
		t.Fatalf("assemble activation release set: %v", err)
	}
	if ttl != 900 || len(members) != 2 || len(fallback) != 1 {
		t.Fatalf("activation release set did not include the whole project or shortest retention window: members=%+v fallback=%+v ttl=%d", members, fallback, ttl)
	}
	if releaseMemberForApp(ProjectReleaseSet{Members: members}, apiAppID) != candidateDeployment ||
		releaseMemberForApp(ProjectReleaseSet{Members: members}, workerAppID) != workerDeployment ||
		fallback[0] != (ProjectReleaseMember{AppID: workerAppID, DeploymentID: workerDeployment}) {
		t.Fatalf("unexpected activation targets: members=%+v fallback=%+v", members, fallback)
	}
}

func TestEnvironmentGitOpsActivationPreservesUnmanagedMembersFromActiveRelease(t *testing.T) {
	projectID, accountID := uuid.NewString(), uuid.NewString()
	apiAppID, workerAppID := uuid.NewString(), uuid.NewString()
	oldAPIDeployment, candidateDeployment, workerDeployment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	releaseID := uuid.NewString()
	store := &MemStore{
		apps: map[string]App{
			apiAppID:    {ID: apiAppID, AccountID: accountID, ProjectID: projectID, Status: AppActive, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}},
			workerAppID: {ID: workerAppID, AccountID: accountID, ProjectID: projectID, Status: AppActive, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}},
		},
		deployments: map[string]Deployment{
			oldAPIDeployment: {ID: oldAPIDeployment, AppID: apiAppID, Scope: "production", Status: DeployLive},
			candidateDeployment: {ID: candidateDeployment, AppID: apiAppID, Scope: "production", Status: DeploySnapshotting,
				EnvironmentWorkloadRuntime: `{}`, EnvironmentWorkloadHeldValue: environmentWorkloadHeldFlag(true)},
			workerDeployment: {ID: workerDeployment, AppID: workerAppID, Scope: "production", Status: DeployLive},
		},
		activeProjectReleaseSets: map[string]string{releaseKey(projectID, "production"): releaseID},
		projectReleaseSets: map[string]ProjectReleaseSet{releaseID: {ID: releaseID, ProjectID: projectID, EnvironmentSlug: "production", Active: true,
			Members: []ProjectReleaseMember{{AppID: apiAppID, DeploymentID: oldAPIDeployment}, {AppID: workerAppID, DeploymentID: workerDeployment}}}},
	}
	source := EnvironmentGitSource{AccountID: accountID, ProjectID: projectID, EnvironmentSlug: "production"}
	graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/api", AppID: apiAppID, CandidateDeploymentID: candidateDeployment,
	}}}

	members, fallback, ttl, err := store.environmentGitOpsReleaseMembersLocked(source, graph, releaseID)
	if err != nil {
		t.Fatalf("assemble activation release set: %v", err)
	}
	if ttl != 3600 || len(members) != 2 || len(fallback) != 0 ||
		releaseMemberForApp(ProjectReleaseSet{Members: members}, apiAppID) != candidateDeployment ||
		releaseMemberForApp(ProjectReleaseSet{Members: members}, workerAppID) != workerDeployment {
		t.Fatalf("activation failed to replace only the managed member: members=%+v fallback=%+v ttl=%d", members, fallback, ttl)
	}
}

func TestEnvironmentGitOpsActivationRejectsWeightedFallback(t *testing.T) {
	projectID, accountID := uuid.NewString(), uuid.NewString()
	appID := uuid.NewString()
	firstID, secondID := uuid.NewString(), uuid.NewString()
	store := &MemStore{
		apps: map[string]App{appID: {ID: appID, AccountID: accountID, ProjectID: projectID, Status: AppActive,
			Manifest: AppManifest{RevisionPinTTLSeconds: 3600}}},
		deployments: map[string]Deployment{
			firstID:  {ID: firstID, AppID: appID, Scope: "production", Status: DeployLive, TrafficPercent: 50},
			secondID: {ID: secondID, AppID: appID, Scope: "production", Status: DeployLive, TrafficPercent: 50},
		},
	}
	source := EnvironmentGitSource{AccountID: accountID, ProjectID: projectID, EnvironmentSlug: "production"}
	_, _, _, err := store.environmentGitOpsReleaseMembersLocked(source, EnvironmentWorkloadGraph{}, "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("weighted fallback err = %v, want ErrConflict", err)
	}
}
