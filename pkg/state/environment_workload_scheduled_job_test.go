package state_test

import (
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsScheduledJobMustMatchReviewedMaterializedContract(t *testing.T) {
	environmentID, appID, jobID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	image := "registry.example/report@sha256:" + strings.Repeat("a", 64)
	graph := state.EnvironmentWorkloadGraph{EnvironmentID: environmentID}
	member := state.EnvironmentWorkloadGraphMember{Resource: "workload/report", AppID: appID, JobID: jobID,
		Variables: map[string]string{"MODE": "safe"}, ExecutionMode: api.ExecutionModeJob, ScheduleConfigured: true, JobSmokeConfigured: true}
	intent := state.EnvironmentWorkloadIntent{AccountID: uuid.NewString(), AppID: appID, EnvironmentID: environmentID, JobID: jobID,
		Source:   &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
		Schedule: &api.EnvironmentJobSchedule{Cron: "15 * * * *", Timezone: "UTC"}, Variables: map[string]string{"MODE": "safe"}}
	job := state.Job{ID: jobID, AccountID: intent.AccountID, Name: state.EnvironmentGitOpsJobName(environmentID, appID), Kind: "recurring",
		ImageRef: image, RAMMB: 512, TaskTimeoutS: 300, MaxParallelism: 1, EnvOverrides: []byte(`{"MODE":"safe"}`), Status: "paused",
		CronSchedule: "15 * * * *", CronTimezone: "UTC", ScheduleRevision: 2, ImageResolvedDigest: "sha256:" + strings.Repeat("a", 64),
		ImageStorageKey: "jobs/immutable.ext4", ImageMaterializationStatus: "ready"}
	if !state.EnvironmentGitOpsScheduledJobMatches(graph, member, intent, job, "paused") {
		t.Fatal("matching materialized Job did not satisfy the activation contract")
	}

	mutations := []struct {
		name   string
		change func(*state.EnvironmentWorkloadIntent, *state.Job)
	}{
		{name: "reviewed image", change: func(intent *state.EnvironmentWorkloadIntent, job *state.Job) {
			intent.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/other@sha256:" + strings.Repeat("b", 64)}
		}},
		{name: "materialized digest", change: func(_ *state.EnvironmentWorkloadIntent, job *state.Job) {
			job.ImageResolvedDigest = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "artifact readiness", change: func(_ *state.EnvironmentWorkloadIntent, job *state.Job) { job.ImageMaterializationStatus = "pending" }},
		{name: "reviewed schedule", change: func(intent *state.EnvironmentWorkloadIntent, _ *state.Job) { intent.Schedule.Cron = "30 * * * *" }},
		{name: "scheduler status", change: func(_ *state.EnvironmentWorkloadIntent, job *state.Job) { job.Status = "active" }},
		{name: "runtime override", change: func(_ *state.EnvironmentWorkloadIntent, job *state.Job) { job.Command = []string{"/bin/sh"} }},
		{name: "unreviewed environment", change: func(_ *state.EnvironmentWorkloadIntent, job *state.Job) {
			job.EnvOverrides = []byte(`{"MODE":"unreviewed"}`)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidateIntent, candidateJob := state.CloneWorkloadIntent(intent), job
			mutation.change(&candidateIntent, &candidateJob)
			if state.EnvironmentGitOpsScheduledJobMatches(graph, member, candidateIntent, candidateJob, "paused") {
				t.Fatal("mutated Job satisfied the reviewed activation contract")
			}
		})
	}
	bindings := map[string]state.EnvironmentScopedServiceBinding{"api": {
		Workload: "api", EnvKey: "API_URL", TargetAppID: uuid.NewString(),
	}}
	boundMember := member
	boundMember.ServiceBindingsConfigured, boundMember.ServiceBindings = true, maps.Clone(bindings)
	boundIntent := state.CloneWorkloadIntent(intent)
	boundIntent.ServiceBindings = maps.Clone(bindings)
	if !state.EnvironmentGitOpsScheduledJobMatches(graph, boundMember, boundIntent, job, "paused") {
		t.Fatal("matching reviewed service bindings did not satisfy the activation contract")
	}
	boundIntent.ServiceBindings["api"] = state.EnvironmentScopedServiceBinding{
		Workload: "api-v2", EnvKey: "API_URL", TargetAppID: bindings["api"].TargetAppID,
	}
	if state.EnvironmentGitOpsScheduledJobMatches(graph, boundMember, boundIntent, job, "paused") {
		t.Fatal("scheduled Job with changed service bindings satisfied activation")
	}
}

func TestEnvironmentWorkloadServingRequiresScheduledRunAfterRelease(t *testing.T) {
	appID, jobID := uuid.NewString(), uuid.NewString()
	targetAppID := uuid.NewString()
	bindings := map[string]state.EnvironmentScopedServiceBinding{"api": {
		Workload: "api", EnvKey: "API_URL", TargetAppID: targetAppID,
	}}
	releaseCreatedAt := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	graph := state.EnvironmentWorkloadGraph{Members: []state.EnvironmentWorkloadGraphMember{{Resource: "workload/report", AppID: appID,
		JobID: jobID, ExecutionMode: api.ExecutionModeJob, ScheduleConfigured: true, JobSmokeConfigured: true,
		ServiceBindingsConfigured: true, ServiceBindings: bindings}}}
	receipt := state.EnvironmentWorkloadServingReceipt{ReleaseCreatedAt: releaseCreatedAt, ExpectedScheduledJobs: state.EnvironmentGitOpsScheduledJobMembers(graph),
		ScheduledJobAcks: []state.EnvironmentWorkloadServingScheduledJobAck{{AppID: appID, JobID: jobID, JobRunID: uuid.NewString(),
			OccurrenceID: uuid.NewString(), AcknowledgedAt: releaseCreatedAt.Add(time.Minute), ServiceBindings: maps.Clone(bindings)}}}
	if !state.EnvironmentWorkloadServingReceiptAcknowledged(receipt) {
		t.Fatal("post-activation scheduled occurrence with the exact service binding did not satisfy the serving receipt")
	}
	receipt.ScheduledJobAcks[0].ServiceBindings["api"] = state.EnvironmentScopedServiceBinding{
		Workload: "api-v2", EnvKey: "API_URL", TargetAppID: targetAppID,
	}
	if state.EnvironmentWorkloadServingReceiptAcknowledged(receipt) {
		t.Fatal("scheduled occurrence with a changed service binding satisfied serving")
	}
	receipt.ScheduledJobAcks[0].ServiceBindings = maps.Clone(bindings)
	receipt.ScheduledJobAcks[0].AcknowledgedAt = releaseCreatedAt.Add(-time.Second)
	if state.EnvironmentWorkloadServingReceiptAcknowledged(receipt) {
		t.Fatal("pre-activation scheduled occurrence satisfied serving")
	}
	receipt.ScheduledJobAcks[0].AcknowledgedAt = releaseCreatedAt.Add(time.Minute)
	receipt.ScheduledJobAcks = append(receipt.ScheduledJobAcks, receipt.ScheduledJobAcks[0])
	if state.EnvironmentWorkloadServingReceiptAcknowledged(receipt) {
		t.Fatal("duplicate scheduled occurrence satisfied serving")
	}
}

func TestEnvironmentGitOpsScheduledServingRequiresExactRunSnapshot(t *testing.T) {
	releaseCreatedAt := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	startedAt, finishedAt := releaseCreatedAt.Add(time.Minute), releaseCreatedAt.Add(2*time.Minute)
	retryMax, timeout, ramMB := 0, 300, 512
	job := state.Job{ID: uuid.NewString(), AccountID: uuid.NewString(), ImageRef: "registry.example/report@sha256:" + strings.Repeat("d", 64),
		ImageResolvedDigest: "sha256:" + strings.Repeat("d", 64), ImageStorageKey: "jobs/report.ext4", ImageMaterializationStatus: "ready",
		RAMMB: ramMB, TaskTimeoutS: timeout, MaxParallelism: 1, RetryMax: retryMax, Command: []string{}, ScheduleRevision: 4,
		EnvOverrides: []byte(`{"MODE":"safe"}`), Status: "active"}
	occurrence := state.ScheduleOccurrence{ID: uuid.NewString(), JobID: job.ID, ScheduleRevision: job.ScheduleRevision,
		Status: "succeeded", CreatedAt: startedAt, StartedAt: &startedAt, FinishedAt: &finishedAt}
	run := state.JobRun{ID: uuid.NewString(), JobID: job.ID, AccountID: job.AccountID, OccurrenceID: occurrence.ID,
		TriggerKind: "scheduled", AggregateStatus: "succeeded", CreatedAt: startedAt, StartedAt: &startedAt, FinishedAt: &finishedAt,
		Tasks: 1, Parallelism: 1, RetryMax: &retryMax, TaskTimeoutS: &timeout, Command: []string{},
		ImageRefSnapshot: job.ImageRef, ImageResolvedDigestSnapshot: job.ImageResolvedDigest, ImageStorageKeySnapshot: job.ImageStorageKey,
		RAMMBSnapshot: &ramMB, EnvOverrides: []byte(`{"MODE":"safe"}`), EffectiveEnvSnapshot: []byte(`{"MODE":"safe"}`),
		ExecutionClass: "standard", FailurePolicy: "continue"}
	occurrence.JobRunID = run.ID
	if !state.EnvironmentGitOpsScheduledJobRunMatches(job, occurrence, run, releaseCreatedAt) {
		t.Fatal("exact scheduled run snapshot did not satisfy serving evidence")
	}

	mutations := []struct {
		name   string
		change func(*state.JobRun)
	}{
		{name: "command", change: func(run *state.JobRun) { run.Command = []string{"/bin/sh"} }},
		{name: "RAM", change: func(run *state.JobRun) { different := ramMB + 1; run.RAMMBSnapshot = &different }},
		{name: "timeout", change: func(run *state.JobRun) { different := timeout + 1; run.TaskTimeoutS = &different }},
		{name: "retry policy", change: func(run *state.JobRun) { different := retryMax + 1; run.RetryMax = &different }},
		{name: "effective environment", change: func(run *state.JobRun) { run.EffectiveEnvSnapshot = []byte(`{"UNREVIEWED":"value"}`) }},
		{name: "run environment", change: func(run *state.JobRun) { run.EnvOverrides = []byte(`{"UNREVIEWED":"value"}`) }},
		{name: "execution class", change: func(run *state.JobRun) { run.ExecutionClass = "flexible" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := run
			mutation.change(&candidate)
			if state.EnvironmentGitOpsScheduledJobRunMatches(job, occurrence, candidate, releaseCreatedAt) {
				t.Fatal("run with a changed execution snapshot satisfied serving evidence")
			}
		})
	}
}

func TestEnvironmentGraphProductionServingSupportsOnlyBoundScheduledJobs(t *testing.T) {
	appID, jobID, candidateID, targetAppID, targetCandidateID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	member := state.EnvironmentWorkloadGraphMember{Resource: "workload/report", AppID: appID, JobID: jobID,
		CandidateDeploymentID: candidateID, ExecutionMode: api.ExecutionModeJob, ScheduleConfigured: true, JobSmokeConfigured: true}
	if !state.EnvironmentGraphSupportsProductionServing(state.EnvironmentWorkloadGraph{Members: []state.EnvironmentWorkloadGraphMember{member}}) {
		t.Fatal("scheduled Job did not expose its production adapter")
	}
	member.ServiceBindingsConfigured = true
	member.ServiceBindings = map[string]state.EnvironmentScopedServiceBinding{"api": {
		Workload: "api", EnvKey: "API_URL", TargetAppID: targetAppID,
	}}
	target := state.EnvironmentWorkloadGraphMember{Resource: "workload/api", AppID: targetAppID, CandidateDeploymentID: targetCandidateID,
		ExecutionMode: api.ExecutionModeService}
	graph := state.EnvironmentWorkloadGraph{Members: []state.EnvironmentWorkloadGraphMember{member, target}}
	if !state.EnvironmentGraphSupportsProductionServing(graph) {
		t.Fatal("service-bound scheduled Job with an exact HTTP target did not expose its production adapter")
	}
	target.ExecutionMode = api.ExecutionModeJob
	graph.Members[1] = target
	if state.EnvironmentGraphSupportsProductionServing(graph) {
		t.Fatal("service-bound scheduled Job accepted a Job target")
	}
}
