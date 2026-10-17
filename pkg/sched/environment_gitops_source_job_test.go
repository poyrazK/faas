package sched

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type sourceJobReleaseFixture struct {
	MemStore       *state.MemStore
	deployment     state.Deployment
	release        state.ProjectReleaseSet
	producerErr    error
	producerChecks int
}

func (f *sourceJobReleaseFixture) DeploymentByID(context.Context, string) (state.Deployment, error) {
	return f.deployment, nil
}
func (f *sourceJobReleaseFixture) ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error) {
	return f.release, nil
}

func (f *sourceJobReleaseFixture) GetFreshDeploymentRuntimeProducerInputs(context.Context, string, string, string) (state.DeploymentRuntimeProducerInputs, error) {
	f.producerChecks++
	return state.DeploymentRuntimeProducerInputs{}, f.producerErr
}

func TestSourceScheduledJobDispatchRequiresFrozenArtifactAndCurrentRelease(t *testing.T) {
	ctx, base := t.Context(), state.NewMemStore()
	account, err := base.CreateAccount(ctx, "source-job-dispatch@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := base.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "source-reports", RepoFullName: "example/reports", InstallID: 42, ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := base.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := base.CreateEnvironmentGitSource(ctx, account.ID, project.ID, env.Slug, state.EnvironmentGitSourceSpec{Provider: "github", RepositoryID: 123, InstallationID: 42, Repository: "example/reports", Ref: "refs/heads/main", ManifestPath: "staging.yaml", Mode: "report", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	appID, depID, jobID, revisionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	buildID, _ := uuid.NewV7()
	commit := strings.Repeat("a", 40)
	source := &api.EnvironmentWorkloadSource{Kind: "source", Directory: "jobs/report"}
	command := []string{"/usr/local/bin/node", "/app/report.js"}
	runtime := map[string]json.RawMessage{"execution_mode": json.RawMessage(`"job"`), "entrypoint": json.RawMessage(`["/usr/local/bin/node","/app/report.js"]`)}
	schedule := &api.EnvironmentJobSchedule{Cron: "15 * * * *", Timezone: "UTC"}
	intent := state.EnvironmentWorkloadIntent{AccountID: account.ID, AppID: appID, EnvironmentID: env.ID, JobID: jobID, Source: source, SourceRevision: commit, Runtime: runtime, Schedule: schedule}
	archive := state.EnvironmentWorkloadSourceArtifact{RevisionID: revisionID, CommitSHA: commit, DefinitionDigest: strings.Repeat("b", 64), BuildID: buildID.String(), Path: "/review/source.tgz", SHA256: strings.Repeat("c", 64), Bytes: 512, LogPath: "/review/build.log"}
	frozen := state.EnvironmentWorkloadRuntime{SourceProvider: "github", SourceRepository: "example/reports", SourceRepositoryID: 123, Source: source, SourceArchive: &archive, SourceID: binding.ID, EnvironmentID: env.ID, RevisionID: revisionID, DefinitionDigest: archive.DefinitionDigest, Generation: 2, IntentVersion: 3, Resource: "workload/report", PlanHash: strings.Repeat("d", 64), AppID: appID, Scope: env.Slug, AppType: state.AppTypeApp, WorkloadClass: state.WorkloadClassJob, Baseline: state.AppManifest{ExecutionMode: "job"}, Runtime: runtime, Schedule: schedule}
	raw, _ := json.Marshal(frozen)
	held := false
	dep := state.Deployment{ID: depID, AppID: appID, Scope: env.Slug, Kind: state.DeploymentKindGitHub, CommitSHA: commit, BuildID: buildID.String(), SourcePath: archive.Path, SourceSHA256: archive.SHA256, SourceBytes: archive.Bytes, SourceRoot: source.Directory, LogPath: archive.LogPath, SourceURL: "https://codeload.github.com/example/reports/tar.gz/" + commit, GitHubInstallationID: 42, RootfsPath: "/review/job.ext4", RootfsKey: "apps/review/job.ext4", RootfsBytes: 4096, Status: state.DeployLive, EnvironmentWorkloadRuntime: string(raw), EnvironmentWorkloadHeldValue: &held}
	job := state.Job{ID: jobID, AccountID: account.ID, Kind: "recurring", ImageRef: "gregale-source://" + env.ID + "/" + appID + "/" + commit, ImageResolvedDigest: "gregale-deployment:" + depID, ImageStorageKey: dep.RootfsKey, ImageMaterializationStatus: "ready", Status: "active", Command: command, CronSchedule: schedule.Cron, CronTimezone: schedule.Timezone, ScheduleRevision: 2, RAMMB: 512, TaskTimeoutS: 300, MaxParallelism: 1, EnvOverrides: json.RawMessage(`{}`)}
	retry, timeout, ram := 0, 300, 512
	run := state.JobRun{ID: uuid.NewString(), JobID: jobID, AccountID: account.ID, Tasks: 1, Parallelism: 1, ExecutionClass: "standard", FailurePolicy: "continue", RetryMax: &retry, TaskTimeoutS: &timeout, RAMMBSnapshot: &ram, Command: command, ImageRefSnapshot: job.ImageRef, ImageResolvedDigestSnapshot: job.ImageResolvedDigest, ImageStorageKeySnapshot: job.ImageStorageKey, EnvOverrides: json.RawMessage(`{}`), EffectiveEnvSnapshot: json.RawMessage(`{}`), CreatedAt: time.Now()}
	f := &sourceJobReleaseFixture{MemStore: base, deployment: dep, release: state.ProjectReleaseSet{ID: uuid.NewString(), AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: env.Slug, Active: true, Members: []state.ProjectReleaseMember{{AppID: appID, DeploymentID: depID}}}}
	e := newEngine(t, statetest.NewMem(f), &fakeVMM{}, &fakeNotifier{}, "test-fc")
	if _, err := dep.ScopedWorkloadRuntime(); err != nil {
		t.Fatalf("invalid fixture: %v", err)
	}
	if err := e.validateEnvironmentGitOpsSourceJob(ctx, job, run, intent); err != nil {
		t.Fatalf("matching source/release rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*state.Job, *state.JobRun, *state.EnvironmentWorkloadIntent)
	}{
		{"old occurrence layer", func(_ *state.Job, r *state.JobRun, _ *state.EnvironmentWorkloadIntent) {
			r.ImageStorageKeySnapshot = "old-layer"
		}},
		{"wrong commit", func(_ *state.Job, _ *state.JobRun, i *state.EnvironmentWorkloadIntent) {
			i.SourceRevision = strings.Repeat("e", 40)
		}},
		{"production command differs", func(j *state.Job, _ *state.JobRun, _ *state.EnvironmentWorkloadIntent) {
			j.Command = []string{"/bin/true"}
		}},
		{"rootfs substituted", func(j *state.Job, r *state.JobRun, _ *state.EnvironmentWorkloadIntent) {
			j.ImageStorageKey = "substitute"
			r.ImageStorageKeySnapshot = j.ImageStorageKey
		}},
		{"paused during boot", func(j *state.Job, _ *state.JobRun, _ *state.EnvironmentWorkloadIntent) { j.Status = "paused" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j, r, i := job, run, intent
			tt.mutate(&j, &r, &i)
			if err := e.validateEnvironmentGitOpsSourceJob(ctx, j, r, i); err == nil {
				t.Fatal("changed source/run admitted")
			}
		})
	}
	t.Run("borrowed signed artifact", func(t *testing.T) {
		pinned := *source
		pinned.BuildArtifactID, pinned.BuildArtifactHash = uuid.NewString(), strings.Repeat("f", 64)
		borrowed := frozen
		borrowed.Source, borrowed.SourceArchive = &pinned, nil
		borrowed.BuiltArtifact = &state.EnvironmentWorkloadBuiltArtifact{ID: pinned.BuildArtifactID, InputHash: pinned.BuildArtifactHash,
			DeploymentID: uuid.NewString(), BuildID: buildID.String(), CommitSHA: commit, Digest: "sha256:" + strings.Repeat("b", 64),
			RootfsPath: dep.RootfsPath, StorageKey: dep.RootfsKey, Bytes: dep.RootfsBytes}
		raw, _ := json.Marshal(borrowed)
		d := dep
		d.BuildID, d.SourcePath, d.SourceSHA256, d.LogPath, d.SourceBytes = "", "", "", "", 0
		d.CommitSHA, d.EnvironmentWorkloadRuntime = strings.Repeat("d", 40), string(raw)
		i, j, r := intent, job, run
		i.Source, i.SourceRevision = &pinned, ""
		j.ImageRef = "gregale-source://" + env.ID + "/" + appID + "/artifact/" + pinned.BuildArtifactID + "/" + pinned.BuildArtifactHash
		r.ImageRefSnapshot = j.ImageRef
		f.deployment = d
		defer func() { f.deployment = dep; f.producerErr = nil }()
		if _, err := d.ScopedWorkloadRuntime(); err != nil {
			t.Fatal("invalid borrowed fixture", err)
		}
		if err := e.validateEnvironmentGitOpsSourceJob(ctx, j, r, i); err != nil || f.producerChecks != 1 {
			t.Fatal("signed artifact was not reauthenticated", err)
		}
		f.producerErr = state.ErrApplicationStandardRuntimeStale
		if err := e.validateEnvironmentGitOpsSourceJob(ctx, j, r, i); !errors.Is(err, f.producerErr) || f.producerChecks != 2 {
			t.Fatal("cached publisher approval survived revocation", err)
		}
		f.producerErr = nil
		for _, mutate := range []func(*state.EnvironmentWorkloadIntent, *state.Job){
			func(i *state.EnvironmentWorkloadIntent, _ *state.Job) { i.SourceRevision = commit },
			func(i *state.EnvironmentWorkloadIntent, _ *state.Job) {
				s := *i.Source
				s.BuildArtifactHash = strings.Repeat("e", 64)
				i.Source = &s
			},
			func(_ *state.EnvironmentWorkloadIntent, j *state.Job) { j.ImageRef = job.ImageRef },
		} {
			changedIntent, changedJob := i, j
			mutate(&changedIntent, &changedJob)
			if err := e.validateEnvironmentGitOpsSourceJob(ctx, changedJob, r, changedIntent); err == nil {
				t.Fatal("mixed build commit/artifact identity admitted")
			}
		}
	})
	f.release.Members[0].DeploymentID = uuid.NewString()
	if err := e.validateEnvironmentGitOpsSourceJob(ctx, job, run, intent); err == nil {
		t.Fatal("superseded active release admitted")
	}
}
