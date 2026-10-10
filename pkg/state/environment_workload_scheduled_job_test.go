package state

import (
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentGitOpsScheduledJobMustMatchReviewedMaterializedContract(t *testing.T) {
	environmentID, appID, jobID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	image := "registry.example/report@sha256:" + strings.Repeat("a", 64)
	graph := EnvironmentWorkloadGraph{EnvironmentID: environmentID}
	member := EnvironmentWorkloadGraphMember{Resource: "workload/report", AppID: appID, JobID: jobID,
		Variables: map[string]string{"MODE": "safe"}, ExecutionMode: api.ExecutionModeJob, ScheduleConfigured: true, JobSmokeConfigured: true}
	intent := EnvironmentWorkloadIntent{AccountID: uuid.NewString(), AppID: appID, EnvironmentID: environmentID, JobID: jobID,
		Source:   &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
		Schedule: &api.EnvironmentJobSchedule{Cron: "15 * * * *", Timezone: "UTC"}, Variables: map[string]string{"MODE": "safe"}}
	job := Job{ID: jobID, AccountID: intent.AccountID, Name: environmentGitOpsJobName(environmentID, appID), Kind: "recurring",
		ImageRef: image, RAMMB: 512, TaskTimeoutS: 300, MaxParallelism: 1, EnvOverrides: []byte(`{"MODE":"safe"}`), Status: "paused",
		CronSchedule: "15 * * * *", CronTimezone: "UTC", ScheduleRevision: 2, ImageResolvedDigest: "sha256:" + strings.Repeat("a", 64),
		ImageStorageKey: "jobs/immutable.ext4", ImageMaterializationStatus: "ready"}
	if !environmentGitOpsScheduledJobMatches(graph, member, intent, job, "paused") {
		t.Fatal("matching materialized Job did not satisfy the activation contract")
	}

	mutations := []struct {
		name   string
		change func(*EnvironmentWorkloadIntent, *Job)
	}{
		{name: "reviewed image", change: func(intent *EnvironmentWorkloadIntent, job *Job) {
			intent.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/other@sha256:" + strings.Repeat("b", 64)}
		}},
		{name: "materialized digest", change: func(_ *EnvironmentWorkloadIntent, job *Job) {
			job.ImageResolvedDigest = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "artifact readiness", change: func(_ *EnvironmentWorkloadIntent, job *Job) { job.ImageMaterializationStatus = "pending" }},
		{name: "reviewed schedule", change: func(intent *EnvironmentWorkloadIntent, _ *Job) { intent.Schedule.Cron = "30 * * * *" }},
		{name: "scheduler status", change: func(_ *EnvironmentWorkloadIntent, job *Job) { job.Status = "active" }},
		{name: "runtime override", change: func(_ *EnvironmentWorkloadIntent, job *Job) { job.Command = []string{"/bin/sh"} }},
		{name: "unreviewed environment", change: func(_ *EnvironmentWorkloadIntent, job *Job) { job.EnvOverrides = []byte(`{"MODE":"unreviewed"}`) }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidateIntent, candidateJob := cloneWorkloadIntent(intent), job
			mutation.change(&candidateIntent, &candidateJob)
			if environmentGitOpsScheduledJobMatches(graph, member, candidateIntent, candidateJob, "paused") {
				t.Fatal("mutated Job satisfied the reviewed activation contract")
			}
		})
	}
	bindings := map[string]EnvironmentScopedServiceBinding{"api": {
		Workload: "api", EnvKey: "API_URL", TargetAppID: uuid.NewString(),
	}}
	boundMember := member
	boundMember.ServiceBindingsConfigured, boundMember.ServiceBindings = true, maps.Clone(bindings)
	boundIntent := cloneWorkloadIntent(intent)
	boundIntent.ServiceBindings = maps.Clone(bindings)
	if !environmentGitOpsScheduledJobMatches(graph, boundMember, boundIntent, job, "paused") {
		t.Fatal("matching reviewed service bindings did not satisfy the activation contract")
	}
	boundIntent.ServiceBindings["api"] = EnvironmentScopedServiceBinding{
		Workload: "api-v2", EnvKey: "API_URL", TargetAppID: bindings["api"].TargetAppID,
	}
	if environmentGitOpsScheduledJobMatches(graph, boundMember, boundIntent, job, "paused") {
		t.Fatal("scheduled Job with changed service bindings satisfied activation")
	}
}

func TestEnvironmentWorkloadServingRequiresScheduledRunAfterRelease(t *testing.T) {
	appID, jobID := uuid.NewString(), uuid.NewString()
	targetAppID := uuid.NewString()
	bindings := map[string]EnvironmentScopedServiceBinding{"api": {
		Workload: "api", EnvKey: "API_URL", TargetAppID: targetAppID,
	}}
	releaseCreatedAt := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{{Resource: "workload/report", AppID: appID,
		JobID: jobID, ExecutionMode: api.ExecutionModeJob, ScheduleConfigured: true, JobSmokeConfigured: true,
		ServiceBindingsConfigured: true, ServiceBindings: bindings}}}
	receipt := EnvironmentWorkloadServingReceipt{ReleaseCreatedAt: releaseCreatedAt, ExpectedScheduledJobs: environmentGitOpsScheduledJobMembers(graph),
		ScheduledJobAcks: []EnvironmentWorkloadServingScheduledJobAck{{AppID: appID, JobID: jobID, JobRunID: uuid.NewString(),
			OccurrenceID: uuid.NewString(), AcknowledgedAt: releaseCreatedAt.Add(time.Minute), ServiceBindings: maps.Clone(bindings)}}}
	if !environmentWorkloadServingReceiptAcknowledged(receipt) {
		t.Fatal("post-activation scheduled occurrence with the exact service binding did not satisfy the serving receipt")
	}
	receipt.ScheduledJobAcks[0].ServiceBindings["api"] = EnvironmentScopedServiceBinding{
		Workload: "api-v2", EnvKey: "API_URL", TargetAppID: targetAppID,
	}
	if environmentWorkloadServingReceiptAcknowledged(receipt) {
		t.Fatal("scheduled occurrence with a changed service binding satisfied serving")
	}
	receipt.ScheduledJobAcks[0].ServiceBindings = maps.Clone(bindings)
	receipt.ScheduledJobAcks[0].AcknowledgedAt = releaseCreatedAt.Add(-time.Second)
	if environmentWorkloadServingReceiptAcknowledged(receipt) {
		t.Fatal("pre-activation scheduled occurrence satisfied serving")
	}
	receipt.ScheduledJobAcks[0].AcknowledgedAt = releaseCreatedAt.Add(time.Minute)
	receipt.ScheduledJobAcks = append(receipt.ScheduledJobAcks, receipt.ScheduledJobAcks[0])
	if environmentWorkloadServingReceiptAcknowledged(receipt) {
		t.Fatal("duplicate scheduled occurrence satisfied serving")
	}
}

func TestEnvironmentGitOpsScheduledServingRequiresExactRunSnapshot(t *testing.T) {
	releaseCreatedAt := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	startedAt, finishedAt := releaseCreatedAt.Add(time.Minute), releaseCreatedAt.Add(2*time.Minute)
	retryMax, timeout, ramMB := 0, 300, 512
	job := Job{ID: uuid.NewString(), AccountID: uuid.NewString(), ImageRef: "registry.example/report@sha256:" + strings.Repeat("d", 64),
		ImageResolvedDigest: "sha256:" + strings.Repeat("d", 64), ImageStorageKey: "jobs/report.ext4", ImageMaterializationStatus: "ready",
		RAMMB: ramMB, TaskTimeoutS: timeout, MaxParallelism: 1, RetryMax: retryMax, Command: []string{}, ScheduleRevision: 4,
		EnvOverrides: []byte(`{"MODE":"safe"}`), Status: "active"}
	occurrence := ScheduleOccurrence{ID: uuid.NewString(), JobID: job.ID, ScheduleRevision: job.ScheduleRevision,
		Status: "succeeded", CreatedAt: startedAt, StartedAt: &startedAt, FinishedAt: &finishedAt}
	run := JobRun{ID: uuid.NewString(), JobID: job.ID, AccountID: job.AccountID, OccurrenceID: occurrence.ID,
		TriggerKind: "scheduled", AggregateStatus: "succeeded", CreatedAt: startedAt, StartedAt: &startedAt, FinishedAt: &finishedAt,
		Tasks: 1, Parallelism: 1, RetryMax: &retryMax, TaskTimeoutS: &timeout, Command: []string{},
		ImageRefSnapshot: job.ImageRef, ImageResolvedDigestSnapshot: job.ImageResolvedDigest, ImageStorageKeySnapshot: job.ImageStorageKey,
		RAMMBSnapshot: &ramMB, EnvOverrides: []byte(`{"MODE":"safe"}`), EffectiveEnvSnapshot: []byte(`{"MODE":"safe"}`),
		ExecutionClass: "standard", FailurePolicy: "continue"}
	occurrence.JobRunID = run.ID
	if !environmentGitOpsScheduledJobRunMatches(job, occurrence, run, releaseCreatedAt) {
		t.Fatal("exact scheduled run snapshot did not satisfy serving evidence")
	}

	mutations := []struct {
		name   string
		change func(*JobRun)
	}{
		{name: "command", change: func(run *JobRun) { run.Command = []string{"/bin/sh"} }},
		{name: "RAM", change: func(run *JobRun) { different := ramMB + 1; run.RAMMBSnapshot = &different }},
		{name: "timeout", change: func(run *JobRun) { different := timeout + 1; run.TaskTimeoutS = &different }},
		{name: "retry policy", change: func(run *JobRun) { different := retryMax + 1; run.RetryMax = &different }},
		{name: "effective environment", change: func(run *JobRun) { run.EffectiveEnvSnapshot = []byte(`{"UNREVIEWED":"value"}`) }},
		{name: "run environment", change: func(run *JobRun) { run.EnvOverrides = []byte(`{"UNREVIEWED":"value"}`) }},
		{name: "execution class", change: func(run *JobRun) { run.ExecutionClass = "flexible" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := run
			mutation.change(&candidate)
			if environmentGitOpsScheduledJobRunMatches(job, occurrence, candidate, releaseCreatedAt) {
				t.Fatal("run with a changed execution snapshot satisfied serving evidence")
			}
		})
	}
}

func TestEnvironmentGraphProductionServingSupportsOnlyBoundScheduledJobs(t *testing.T) {
	appID, jobID, candidateID, targetAppID, targetCandidateID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	member := EnvironmentWorkloadGraphMember{Resource: "workload/report", AppID: appID, JobID: jobID,
		CandidateDeploymentID: candidateID, ExecutionMode: api.ExecutionModeJob, ScheduleConfigured: true, JobSmokeConfigured: true}
	if !environmentGraphSupportsProductionServing(EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{member}}) {
		t.Fatal("scheduled Job did not expose its production adapter")
	}
	member.ServiceBindingsConfigured = true
	member.ServiceBindings = map[string]EnvironmentScopedServiceBinding{"api": {
		Workload: "api", EnvKey: "API_URL", TargetAppID: targetAppID,
	}}
	target := EnvironmentWorkloadGraphMember{Resource: "workload/api", AppID: targetAppID, CandidateDeploymentID: targetCandidateID,
		ExecutionMode: api.ExecutionModeService}
	graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{member, target}}
	if !environmentGraphSupportsProductionServing(graph) {
		t.Fatal("service-bound scheduled Job with an exact HTTP target did not expose its production adapter")
	}
	target.ExecutionMode = api.ExecutionModeJob
	graph.Members[1] = target
	if environmentGraphSupportsProductionServing(graph) {
		t.Fatal("service-bound scheduled Job accepted a Job target")
	}
}
