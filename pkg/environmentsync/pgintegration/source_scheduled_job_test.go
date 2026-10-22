package pgintegration_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func sourceScheduledJobFixture(t *testing.T, basic gitOpsTestStore, provider, kind string, mapped ...bool) (state.EnvironmentGitOpsLease, environmentsync.DesiredState, state.EnvironmentWorkloadIntent, environmentsync.Plan) {
	t.Helper()
	ctx := t.Context()
	account, err := basic.CreateAccount(ctx, "source-schedule@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := basic.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "reports", RepoFullName: "example/reports", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	spec := state.EnvironmentGitSourceSpec{Provider: provider, RepositoryID: 123, InstallationID: 42, Repository: "example/reports", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual", Prune: true}
	if provider == "gitlab" {
		spec.InstallationID = 0
		if _, err := basic.(state.GitLabOAuthStore).PutGitLabOAuthConnection(ctx, state.GitLabOAuthConnection{AccountID: account.ID, GitLabUserID: 41, Username: "reviewer", CredentialsSealed: []byte("test-sealed"), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	source, err := basic.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", spec)
	if err != nil {
		t.Fatal(err)
	}
	appSlug := ""
	if len(mapped) != 0 && mapped[0] {
		app, err := basic.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "report-job", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassJob,
			RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: "job"}})
		if err != nil {
			t.Fatal(err)
		}
		appSlug = app.Slug
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
		"report": {App: appSlug, Source: &api.EnvironmentWorkloadSource{Kind: kind, Directory: "jobs/report"}, Runtime: json.RawMessage(`{"execution_mode":"job","entrypoint":["/usr/local/bin/node","/app/report.js"]}`), JobSmoke: &api.EnvironmentJobSmoke{Command: []string{"/bin/true"}, TimeoutSeconds: 30}, Schedule: &api.EnvironmentJobSchedule{Cron: "15 * * * *", Timezone: "UTC"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	if appSlug != "" {
		adoptWorkloadIntent(t, basic.(intentTestStore), source)
	}
	lease, err := basic.ClaimEnvironmentGitOps(ctx, "source-scheduler", time.Now(), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	intentStore := basic.(state.EnvironmentGitOpsIntentStore)
	plan := planForStore(t, intentStore, lease, desired)
	if !plan.CanApply() {
		t.Fatalf("source schedule blocked: %+v", plan)
	}
	if _, err := basic.(state.EnvironmentGitOpsWorkloadCreationStore).PrepareEnvironmentGitOpsWorkloads(ctx, lease, plan); err != nil {
		t.Fatal(err)
	}
	if appSlug != "" {
		if _, err := intentStore.ApplyEnvironmentGitOps(ctx, lease, planForStore(t, intentStore, lease, desired)); err != nil {
			t.Fatal(err)
		}
	}
	apps, err := basic.ListApps(ctx, account.ID)
	if err != nil || len(apps) != 1 {
		t.Fatalf("reserved apps: %d %v", len(apps), err)
	}
	// Existing release-set activation requires an explicit retention opt-in.
	// Configure it through the normal app API before reviewing build inputs.
	manifest := apps[0].Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := basic.UpdateApp(ctx, apps[0].ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	intent, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(ctx, account.ID, apps[0].ID, source.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	return lease, desired, intent, planForStore(t, intentStore, lease, desired)
}

func qualifyScheduledSourceJob(t *testing.T, basic gitOpsTestStore, request state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	ctx := t.Context()
	app, err := basic.AppByID(ctx, request.AppID)
	if err != nil {
		t.Fatal(err)
	}
	placement := qualificationPlacement(t, basic, 4096)
	// Qualification admission must match the workload's actual plan/runtime RAM.
	placement.RAMMB = app.RAMMB
	claims, err := basic.(state.EnvironmentGitOpsQualificationGraphDispatchStore).ClaimEnvironmentWorkloadQualificationGraphForNode(ctx, request.GraphID, placement.NodeID, "source-job-qualifier", time.Minute)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim graph: %d %v", len(claims), err)
	}
	claimed := claims[0]
	admitted, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(ctx, claimed, placement)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	executor := basic.(state.EnvironmentQualificationExecutionStore)
	if err := executor.MarkEnvironmentQualificationDispatched(ctx, claimed, admitted.Execution); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	inputs := state.RuntimeConfigInputs{Scope: request.FrozenInputs.Scope, Boundary: time.Now().UTC(), Variables: request.FrozenInputs.Variables, SecretRefs: map[string]string{}, SecretVersions: map[string]int64{}}
	_, err = basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(ctx, claimed, state.EnvironmentWorkloadQualificationRuntime{NodeID: placement.NodeID, WakeID: placement.WakeID, Netns: "source-job-qualification", HostIP: "10.0.0.1", GuestUID: 20001, Inputs: inputs})
	if err != nil {
		t.Fatalf("publish runtime: %v", err)
	}
	if _, err := basic.(state.EnvironmentQualificationConfigReceiptStore).RecordEnvironmentQualificationConfigReceipt(ctx, claimed, admitted.Execution, strings.Repeat("b", 64)); err != nil {
		t.Fatalf("config receipt: %v", err)
	}
	if err := executor.RetireEnvironmentQualificationExecution(ctx, admitted.Execution, qualificationNativeProof()); err != nil {
		t.Fatalf("retire: %v", err)
	}
	evidence, err := state.NewEnvironmentQualificationJobSmokeEvidence(claimed, admitted.Execution.InstanceID, 0, "succeeded", 0)
	if err != nil {
		t.Fatalf("smoke evidence: %v", err)
	}
	if _, err := basic.(state.EnvironmentQualificationJobSmokeReceiptStore).RecordEnvironmentQualificationJobSmokeReceipt(ctx, claimed, evidence); err != nil {
		t.Fatalf("smoke receipt: %v", err)
	}
}

func TestGitOpsSourceScheduledJobsPinQualifiedBuildAndOccurrence(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, kind := range []string{"source", "dockerfile"} {
			t.Run(provider+"/"+kind, func(t *testing.T) {
				stores(t, func(t *testing.T, basic gitOpsTestStore) {
					ctx := t.Context()
					lease, desired, intent, plan := sourceScheduledJobFixture(t, basic, provider, kind)
					job, err := basic.JobGetByID(ctx, intent.JobID)
					if err != nil || job.Status != "paused" || job.ImageMaterializationStatus != "pending" || !slices.Equal(job.Command, []string{"/usr/local/bin/node", "/app/report.js"}) || intent.SourceRevision != lease.Revision.CommitSHA {
						t.Fatalf("source job binding: %+v %v", job, err)
					}
					images := basic.(state.JobImageMaterializationStore)
					pending, err := images.JobListPendingImageMaterialization(ctx, 10)
					if err != nil || len(pending) != 0 {
						t.Fatalf("source schedule entered OCI queue: %d %v", len(pending), err)
					}
					claims := basic.(state.JobImageMaterializationClaimer)
					if _, err := claims.JobClaimImageMaterialization(ctx, job.ID, "oci-worker", time.Minute); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("direct OCI claim accepted: %v", err)
					}
					if _, err := images.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("f", 64), "wrong-layer", ""); err == nil {
						t.Fatal("generic image publisher replaced source artifact")
					}
					preparation := basic.(state.EnvironmentGitOpsPreparationStore)
					requests, err := preparation.EnvironmentGitOpsSourceRequests(ctx, lease, plan)
					if err != nil || len(requests) != 1 || requests[0].CommitSHA != lease.Revision.CommitSHA {
						t.Fatalf("reviewed archive request: %+v %v", requests, err)
					}
					artifact := sourceArtifact(t, requests[0])
					wrong := artifact
					wrong.CommitSHA = strings.Repeat("c", 40)
					if _, err := preparation.PrepareEnvironmentGitOpsCandidates(ctx, lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: wrong}); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("wrong commit archive accepted: %v", err)
					}
					candidates, err := preparation.PrepareEnvironmentGitOpsCandidates(ctx, lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: artifact})
					if err != nil || len(candidates) != 1 {
						t.Fatalf("source candidate: %+v %v", candidates, err)
					}
					dep, err := basic.DeploymentByID(ctx, candidates[0].DeploymentID)
					if err != nil || dep.Status != state.DeployBuilding || !dep.EnvironmentWorkloadHeld() {
						t.Fatalf("build escaped hold: %+v %v", dep, err)
					}
					activator := basic.(state.EnvironmentGitOpsGraphActivationStore)
					if _, active, err := activator.ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan); err != nil || active {
						t.Fatalf("unfinished build activated: %v %v", active, err)
					}
					retry, err := preparation.PrepareEnvironmentGitOpsCandidates(ctx, lease, plan, nil)
					if err != nil || len(retry) != 1 || retry[0].DeploymentID != dep.ID || retry[0].BuildID != dep.BuildID {
						t.Fatalf("restart duplicated build: %+v %v", retry, err)
					}
					claim, err := basic.ClaimQueuedBuild(ctx, dep.BuildID)
					if err != nil {
						t.Fatal(err)
					}
					if err := basic.CompleteBuild(ctx, claim, "/reviewed-image.tar", "source-image", 512, state.BuildProvenance{BuildID: dep.BuildID, SourceSHA256: artifact.SHA256, SourceURL: dep.SourceURL, CommitSHA: dep.CommitSHA, Plan: "pro", BuilderNodeID: "source-job-builder", StartedAt: claim.StartedAt, FinishedAt: time.Now()}); err != nil {
						t.Fatal(err)
					}
					key := "apps/" + dep.AppID + "/" + dep.ID + ".ext4"
					if err := basic.SetDeploymentRootfs(ctx, dep.ID, "/reviewed-job.ext4", key, 4096); err != nil {
						t.Fatal(err)
					}
					if err := basic.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
						t.Fatal(err)
					}
					graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(ctx, lease, plan)
					if err != nil || graph.Phase != "prepared" {
						t.Fatalf("prepare graph: %+v %v", graph, err)
					}
					qualification, err := basic.(state.EnvironmentGitOpsQualificationStore).QueueEnvironmentGitOpsQualification(ctx, lease, plan)
					if err != nil || len(qualification) != 1 {
						t.Fatalf("qualification: %+v %v", qualification, err)
					}
					if _, active, err := activator.ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan); err != nil || active {
						t.Fatalf("unqualified source activated: %v %v", active, err)
					}
					qualifyScheduledSourceJob(t, basic, qualification[0])
					release, active, err := activator.ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan)
					if err != nil || !active || len(release.Members) != 1 {
						evidence, _ := basic.(state.EnvironmentGitOpsActivationEvidenceStore).EnvironmentGitOpsActivationEvidence(ctx, lease, plan)
						t.Fatalf("qualified source activation: %+v %v %v evidence=%+v", release, active, err, evidence)
					}
					current, err := basic.JobGetByID(ctx, job.ID)
					if err != nil || current.Status != "active" || current.ImageStorageKey != key || current.ScheduleRevision <= job.ScheduleRevision || current.ImageResolvedDigest != "gregale-deployment:"+dep.ID {
						t.Fatalf("qualified artifact binding: %+v %v", current, err)
					}
					dep, err = basic.DeploymentByID(ctx, dep.ID)
					if err != nil || !state.EnvironmentGitOpsSourceJobArtifactMatches(intent, current, dep) {
						t.Fatalf("source identity lost: %v", err)
					}
					changed := intent
					changed.SourceRevision = strings.Repeat("d", 40)
					if state.EnvironmentGitOpsSourceJobArtifactMatches(changed, current, dep) {
						t.Fatal("another commit borrowed qualified artifact")
					}
					serving, ready, err := basic.(state.EnvironmentGitOpsWorkloadServingStore).PrepareEnvironmentGitOpsWorkloadServing(ctx, lease, plan, nil)
					if err != nil || ready || len(serving.ScheduledJobAcks) != 0 {
						t.Fatalf("activation claimed occurrence proof: %+v %v %v", serving, ready, err)
					}
					firedAt := time.Now().UTC()
					run, created, err := basic.(state.JobScheduleOccurrenceStore).JobRunCreateScheduledOccurrence(ctx, job.ID, current.CronSchedule, current.CronTimezone, current.LastScheduledAt, firedAt, state.JobScheduledOccurrenceOptions{ScheduledFor: firedAt, ScheduleRevision: current.ScheduleRevision})
					if err != nil || !created || run.ImageStorageKeySnapshot != key || run.ImageResolvedDigestSnapshot != current.ImageResolvedDigest || !slices.Equal(run.Command, current.Command) {
						t.Fatalf("occurrence artifact snapshot: %+v %v", run, err)
					}
					again, active, err := activator.ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan)
					if err != nil || !active || again.ID != release.ID {
						t.Fatalf("activation retry changed release: %+v %v %v", again, active, err)
					}
					placement := qualificationPlacement(t, basic, 4096)
					instanceID, token := uuid.NewString(), uuid.NewString()
					if _, err := basic.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, 0, string(state.StateColdBooting), current.RAMMB, placement.NodeID, placement.WakeID, token, time.Now().Add(time.Minute), placement.NodeID); err != nil {
						t.Fatalf("claim occurrence: %v", err)
					}
					if _, err := basic.JobRunRecompute(ctx, run.ID); err != nil {
						t.Fatal(err)
					}
					if err := basic.JobTaskMarkTerminal(ctx, run.ID, 0, "succeeded", 0, "succeeded", "", time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					if _, err := basic.JobRunRecompute(ctx, run.ID); err != nil {
						t.Fatal(err)
					}
					served, ready, err := basic.(state.EnvironmentGitOpsWorkloadServingStore).PrepareEnvironmentGitOpsWorkloadServing(ctx, lease, plan, nil)
					if err != nil || !ready || len(served.ScheduledJobAcks) != 1 || served.ScheduledJobAcks[0].JobRunID != run.ID {
						t.Fatalf("source occurrence did not prove serving: %+v %v %v", served, ready, err)
					}
					desired.Definition.Workloads["report"] = api.EnvironmentWorkload{Source: &api.EnvironmentWorkloadSource{Kind: kind, Directory: "jobs/report"}, Runtime: json.RawMessage(`{"execution_mode":"job"}`), Schedule: &api.EnvironmentJobSchedule{Cron: "15 * * * *", Timezone: "UTC"}}
					missingCommand, err := environmentsync.Compile(desired.Definition)
					if err != nil {
						t.Fatal(err)
					}
					observed, err := basic.(state.EnvironmentGitOpsPreviewStore).PreviewEnvironmentGitOpsCommit(ctx, lease.Source, strings.Repeat("e", 40), missingCommand)
					if err != nil || observed.Plan.CanApply() {
						t.Fatalf("source schedule without reviewed argv accepted: %+v %v", observed, err)
					}
				})
			})
		}
	}
}

func TestGitOpsSourceScheduleFailedSupersededAndRollbackBuildsStayHeld(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		ctx := t.Context()
		lease, desired, intent, plan := sourceScheduledJobFixture(t, basic, "github", "source")
		preparation := basic.(state.EnvironmentGitOpsPreparationStore)
		prepare := func(lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) state.EnvironmentWorkloadCandidate {
			t.Helper()
			requests, err := preparation.EnvironmentGitOpsSourceRequests(ctx, lease, plan)
			if err != nil || len(requests) != 1 {
				t.Fatalf("fresh source request: %+v %v", requests, err)
			}
			candidates, err := preparation.PrepareEnvironmentGitOpsCandidates(ctx, lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: sourceArtifact(t, requests[0])})
			if err != nil || len(candidates) != 1 {
				t.Fatalf("fresh source candidate: %+v %v", candidates, err)
			}
			return candidates[0]
		}
		failed := prepare(lease, plan)
		if err := basic.UpdateDeploymentStatus(ctx, failed.DeploymentID, state.DeployFailed, "fixture build failure"); err != nil {
			t.Fatal(err)
		}
		graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(ctx, lease, plan)
		if err != nil || graph.Phase != "failed" {
			t.Fatalf("failed build graph: %+v %v", graph, err)
		}
		if _, active, err := basic.(state.EnvironmentGitOpsGraphActivationStore).ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan); err != nil || active {
			t.Fatalf("failed build activated: %v %v", active, err)
		}
		originalLease := lease
		var previous state.EnvironmentWorkloadCandidate
		for _, sha := range []string{strings.Repeat("b", 40), strings.Repeat("a", 40)} {
			source, err := basic.EnvironmentGitSource(ctx, lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := basic.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, sha)); err != nil {
				t.Fatal(err)
			}
			lease, err = basic.ClaimEnvironmentGitOps(ctx, uuid.NewString(), time.Now(), 5*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, active, err := basic.(state.EnvironmentGitOpsGraphActivationStore).ActivateEnvironmentGitOpsWorkloadGraph(ctx, originalLease, plan); err == nil || active {
				t.Fatalf("superseded graph activated: %v %v", active, err)
			}
			intentStore := basic.(state.EnvironmentGitOpsIntentStore)
			if _, err := intentStore.ApplyEnvironmentGitOps(ctx, lease, planForStore(t, intentStore, lease, desired)); err != nil {
				t.Fatal(err)
			}
			current, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(ctx, intent.AccountID, intent.AppID, intent.EnvironmentID)
			if err != nil || current.JobID != intent.JobID || current.SourceRevision != sha {
				t.Fatalf("recovery changed Job identity: %+v %v", current, err)
			}
			job, err := basic.JobGetByID(ctx, intent.JobID)
			if err != nil || job.Status != "paused" || job.ImageStorageKey != "" || job.ImageResolvedDigest != "" {
				t.Fatalf("recovery borrowed old artifact: %+v %v", job, err)
			}
			next := prepare(lease, planForStore(t, intentStore, lease, desired))
			if next.DeploymentID == failed.DeploymentID || next.BuildID == failed.BuildID || next.DeploymentID == previous.DeploymentID {
				t.Fatal("recovery reused a failed or superseded build")
			}
			previous = next
		}
	})
}
