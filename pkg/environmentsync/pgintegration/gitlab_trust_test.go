package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"github.com/onebox-faas/faas/pkg/gitlabsource"
	"github.com/onebox-faas/faas/pkg/state"
)

func bindGitLabSource(t *testing.T, store gitOpsTestStore, source state.EnvironmentGitSource, policy string) state.EnvironmentGitSource {
	t.Helper()
	if _, err := store.(state.GitLabOAuthStore).PutGitLabOAuthConnection(t.Context(), state.GitLabOAuthConnection{AccountID: source.AccountID, GitLabUserID: 41, Username: "reviewer", CredentialsSealed: []byte("test-sealed"), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if source.Spec.Mode != "report" {
		var err error
		source, err = store.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "report"})
		if err != nil {
			t.Fatal(err)
		}
	}
	spec := state.EnvironmentGitSourceSpec{Provider: "gitlab", RepositoryID: 41, Repository: "team/subgroup/shop", Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: policy}
	result, err := store.(state.EnvironmentGitOpsLifecycleStore).RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestGitLabProtectedApprovalIsAtomicAndProviderIsolated(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seed(t, store)
		source = bindGitLabSource(t, store, source, "protected_branch")
		sha := strings.Repeat("a", 40)
		if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, sha)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("manual bypass: %v", err)
		}
		polls := store.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		evidence := reviewedMergeFixture(source, sha)
		evidence.ReviewedDefinitionDigest = desired.Digest
		result := state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest, Desired: &desired, Approval: &evidence}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, result, now, now.Add(time.Minute)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("GitHub trust crossed provider: %v", err)
		}
		evidence.Profile = gitapproval.GitLabReviewedMergeProfile
		evidence.Policy.Profile = gitapproval.GitLabProtectedBranchProfile
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, result, now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || current.Generation != source.Generation+1 || current.ApprovedRevisionID == "" {
			t.Fatalf("approval was not atomic: %+v %v", current, err)
		}
		record, err := store.(state.EnvironmentGitApprovalStore).EnvironmentGitRevisionApproval(t.Context(), source.AccountID, source.ID, current.ApprovedRevisionID)
		if err != nil || record.Evidence.Profile != gitapproval.GitLabReviewedMergeProfile || record.Evidence.Policy.RepositoryID != 41 {
			t.Fatalf("provider receipt: %+v %v", record, err)
		}
		job, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
		if err != nil || job.Revision.ApprovedBy != "gitlab:protected_branch" {
			t.Fatalf("provider attribution: %+v %v", job, err)
		}
		// Loss of provider access changes freshness, never the last approval.
		later := now.Add(time.Minute)
		retry, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), later, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), retry, state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_unavailable"}, later, later.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		failed, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || failed.ApprovedRevisionID != current.ApprovedRevisionID || failed.Generation != current.Generation {
			t.Fatalf("outage discarded approval: %+v %v", failed, err)
		}
	})
}

func TestGitLabAdoptionAndCandidatesRetainProjectCommitAndBuildIdentity(t *testing.T) {
	for _, name := range []string{"source", "dockerfile", "function", "unverified upload", "foreign project", "foreign provider", "foreign path", "mutable commit"} {
		t.Run(name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				appType := state.AppTypeApp
				valid := name == "source" || name == "dockerfile" || name == "function"
				if name == "function" {
					appType = state.AppTypeFunction
				}
				store, old, desired, app, previous, _ := workloadIntentFixtureType(t, basic, "report", appType)
				source := bindGitLabSource(t, basic, old, "manual")
				sha := strings.Repeat("b", 40)
				provenance := gitlabsource.ProvenanceURL(41, source.Spec.Repository, sha)
				switch name {
				case "foreign project":
					provenance = gitlabsource.ProvenanceURL(42, source.Spec.Repository, sha)
				case "foreign provider":
					provenance = "github://" + source.Spec.Repository + "@" + sha
				case "foreign path":
					provenance = gitlabsource.ProvenanceURL(41, "other/subgroup/shop", sha)
				case "mutable commit":
					provenance = strings.Replace(provenance, sha, "main", 1)
				}
				manifest := app.Manifest
				sourceIntent := &api.EnvironmentWorkloadSource{Kind: "source", Directory: "apps/api"}
				if name == "function" {
					sourceIntent.Kind, sourceIntent.Runtime = "function", "node22"
				}
				if name == "dockerfile" {
					manifest.BuildDockerfile = "Dockerfile"
					sourceIntent.Kind = "dockerfile"
					sourceIntent.Dockerfile = "Dockerfile"
					if _, err := basic.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
						t.Fatal(err)
					}
				}

				if !valid {
					if err := basic.UpdateDeploymentStatus(t.Context(), previous.ID, state.DeploySuperseded, ""); err != nil {
						t.Fatal(err)
					}
					live, err := basic.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, SourceURL: provenance, CommitSHA: sha, SourceRoot: "apps/api"})
					if err != nil {
						t.Fatal(err)
					}
					if err := basic.UpdateDeploymentStatus(t.Context(), live.ID, state.DeployLive, ""); err != nil {
						t.Fatal(err)
					}
				}

				w := desired.Definition.Workloads["api"]
				w.Source = sourceIntent
				desired.Definition.Workloads["api"] = w
				desired, err := environmentsync.Compile(desired.Definition)
				if err != nil {
					t.Fatal(err)
				}
				source, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, sha))
				if err != nil {
					t.Fatal(err)
				}
				plan, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !valid {
					if plan.CanApply() {
						t.Fatalf("foreign provenance adopted: %+v", plan)
					}
					return
				}
				if !plan.CanApply() {
					t.Fatalf("valid GitLab adoption blocked: %+v", plan)
				}
				if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, plan.Hash); err != nil {
					t.Fatal(err)
				}
				intent := scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID)
				if intent.Source == nil || intent.Source.Kind != "image" {
					t.Fatalf("adoption replaced inherited image before reconciliation: %+v", intent)
				}

				source, err = basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "enforce"})
				if err != nil {
					t.Fatal(err)
				}
				lease, err := basic.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), 3*time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				plan = claimedIntentPlan(t, store, lease, desired)
				if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
					t.Fatal(err)
				}
				plan = claimedIntentPlan(t, store, lease, desired)
				intent = scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID)
				if intent.Source == nil || *intent.Source != *sourceIntent || intent.SourceRevision != sha {
					t.Fatalf("reconciled source lost GitLab commit: %+v", intent)
				}
				preparer := basic.(state.EnvironmentGitOpsPreparationStore)
				requests, err := preparer.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
				if err != nil || len(requests) != 1 {
					t.Fatalf("source requests: %+v %v", requests, err)
				}
				artifact := sourceArtifact(t, requests[0])
				candidates, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: artifact})
				if err != nil || len(candidates) != 1 {
					t.Fatalf("prepare: %+v %v", candidates, err)
				}
				dep, err := basic.DeploymentByID(t.Context(), candidates[0].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				frozen, err := dep.ScopedWorkloadRuntime()
				if err != nil || dep.Kind != state.DeploymentKindTarball || dep.SourceURL != provenance || dep.CommitSHA != sha || frozen.SourceProvider != "gitlab" || frozen.SourceRepositoryID != 41 || !dep.EnvironmentWorkloadHeld() {
					t.Fatalf("GitLab candidate attribution: %+v %v", dep, err)
				}
				build, err := basic.BuildByID(t.Context(), artifact.BuildID)
				if err != nil || build.Kind != state.DeploymentKindTarball || build.DeploymentID != dep.ID {
					t.Fatalf("build attribution: %+v %v", build, err)
				}
				tampered := dep
				tampered.SourceURL = gitlabsource.ProvenanceURL(42, source.Spec.Repository, sha)
				if _, err := tampered.ScopedWorkloadRuntime(); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("candidate borrowed another GitLab project: %v", err)
				}
				// Provider metadata is frozen alongside the archive, never inferred later.
				frozen.SourceProvider = "github"
				raw, _ := json.Marshal(frozen)
				tampered = dep
				tampered.EnvironmentWorkloadRuntime = string(raw)
				if _, err := tampered.ScopedWorkloadRuntime(); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("candidate crossed providers: %v", err)
				}
				retry, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, nil)
				if err != nil || len(retry) != 1 || retry[0].DeploymentID != dep.ID || retry[0].BuildID != build.ID {
					t.Fatalf("retry changed durable work: %+v %v", retry, err)
				}
				claim, err := basic.(state.Store).ClaimQueuedBuild(t.Context(), build.ID)
				if err != nil {
					t.Fatal(err)
				}
				buildReceipt := state.BuildProvenance{BuildID: build.ID, SourceSHA256: artifact.SHA256, SourceURL: dep.SourceURL, CommitSHA: sha,
					Plan: "pro", BuilderNodeID: "gitlab-source-builder", StartedAt: claim.StartedAt, FinishedAt: time.Now()}
				if err := basic.(state.Store).CompleteBuild(t.Context(), claim, "/reviewed-gitlab-image.tar", "reviewed-gitlab-image-key", 512, buildReceipt); err != nil {
					t.Fatalf("GitLab artifact completion: %v", err)
				}
				completed, err := store.DeploymentByID(t.Context(), dep.ID)
				if err != nil || !completed.EnvironmentWorkloadHeld() || completed.Status != state.DeployBuilding {
					t.Fatalf("completion changed graph authority: %+v %v", completed, err)
				}
				if _, err := completed.ScopedWorkloadRuntime(); err != nil {
					t.Fatalf("completion changed frozen GitLab identity: %v", err)
				}
				if err := basic.(state.Store).MarkDeploymentLive(t.Context(), dep.ID); err == nil {
					t.Fatal("completed GitLab build bypassed graph qualification")
				}
			})
		})
	}
}
