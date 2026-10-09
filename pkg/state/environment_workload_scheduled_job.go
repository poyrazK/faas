package state

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// environmentGitOpsScheduledJobMatches binds the persisted Job to the exact
// reviewed workload intent and requires its immutable image artifact to be
// ready before activation (or to remain ready while serving).
func environmentGitOpsScheduledJobMatches(graph EnvironmentWorkloadGraph, member EnvironmentWorkloadGraphMember,
	intent EnvironmentWorkloadIntent, job Job, status string) bool {
	if member.ExecutionMode != api.ExecutionModeJob || !member.ScheduleConfigured || !member.JobSmokeConfigured ||
		!canonicalStateUUID(member.JobID) || member.JobID != intent.JobID || member.AppID != intent.AppID ||
		intent.EnvironmentID != graph.EnvironmentID || intent.Source == nil || intent.Source.Kind != "image" ||
		intent.Schedule == nil || job.ID != member.JobID || job.AccountID != intent.AccountID || job.Status != status ||
		job.Name != environmentGitOpsJobName(graph.EnvironmentID, member.AppID) || job.Kind != "recurring" ||
		job.ImageRef != intent.Source.Image || job.CronSchedule != intent.Schedule.Cron || job.CronTimezone != intent.Schedule.Timezone ||
		!reflect.DeepEqual(job.SchedulePolicy, intent.Schedule.SchedulePolicy) ||
		!reflect.DeepEqual(job.FailureRules, intent.Schedule.FailureRules) || job.MaxParallelism != 1 || job.RetryMax != 0 ||
		job.RAMMB <= 0 || job.TaskTimeoutS <= 0 || job.ScheduleRevision <= 0 || len(job.Command) != 0 {
		return false
	}
	if member.ServiceBindingsConfigured != (len(intent.ServiceBindings) != 0) || len(member.ServiceBindings) != len(intent.ServiceBindings) ||
		len(intent.ServiceBindings) != 0 && !reflect.DeepEqual(member.ServiceBindings, intent.ServiceBindings) {
		return false
	}
	if !environmentGitOpsJobEnvironmentMatches(job.EnvOverrides, member.Variables) ||
		!maps.Equal(intent.Variables, member.Variables) {
		return false
	}
	wantDigest, ok := immutableEnvironmentJobImageDigest(intent.Source.Image)
	if !ok || job.ImageMaterializationStatus != "ready" || strings.TrimSpace(job.ImageStorageKey) == "" ||
		job.ImageResolvedDigest != wantDigest {
		return false
	}
	return true
}

func immutableEnvironmentJobImageDigest(image string) (string, bool) {
	_, digest, ok := strings.Cut(image, "@")
	if !ok || !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return "", false
	}
	for _, char := range digest[len("sha256:"):] {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return "", false
			}
		}
	}
	return digest, true
}

// environmentGitOpsScheduledJobRunMatches ensures serving evidence describes
// an execution of the exact managed Job contract. Image snapshots alone are
// insufficient: JobRun also freezes command, resources, retry/timeout policy,
// and effective environment at dispatch time.
func environmentGitOpsScheduledJobRunMatches(job Job, occurrence ScheduleOccurrence, run JobRun, releaseCreatedAt time.Time) bool {
	if releaseCreatedAt.IsZero() || occurrence.ID == "" || occurrence.JobID != job.ID || occurrence.JobRunID != run.ID ||
		occurrence.ScheduleRevision != job.ScheduleRevision || occurrence.Status != "succeeded" || occurrence.CreatedAt.Before(releaseCreatedAt) ||
		occurrence.StartedAt == nil || occurrence.StartedAt.Before(releaseCreatedAt) || occurrence.FinishedAt == nil ||
		occurrence.FinishedAt.Before(releaseCreatedAt) || run.ID == "" || run.JobID != job.ID ||
		run.AccountID != job.AccountID || run.OccurrenceID != occurrence.ID || run.TriggerKind != "scheduled" ||
		run.AggregateStatus != "succeeded" || run.CreatedAt.Before(releaseCreatedAt) || run.StartedAt == nil ||
		run.StartedAt.Before(releaseCreatedAt) || run.FinishedAt == nil ||
		run.FinishedAt.Before(releaseCreatedAt) || occurrence.FinishedAt.Before(*run.FinishedAt) ||
		run.RetryMax == nil || *run.RetryMax != job.RetryMax || run.TaskTimeoutS == nil || *run.TaskTimeoutS != job.TaskTimeoutS ||
		run.Tasks != 1 || run.Parallelism != job.MaxParallelism || run.ExecutionClass != "standard" || run.FailurePolicy != "continue" ||
		!slices.Equal(run.Command, job.Command) || run.ImageRefSnapshot != job.ImageRef ||
		run.ImageResolvedDigestSnapshot != job.ImageResolvedDigest || run.ImageStorageKeySnapshot != job.ImageStorageKey ||
		run.RAMMBSnapshot == nil || *run.RAMMBSnapshot != job.RAMMB ||
		!reflect.DeepEqual(run.FailureRules, job.FailureRules) || run.InputManifestVersion != 0 || run.InputDigest != "" ||
		run.InputManifestURI != "" || run.InputManifestSHA256 != "" {
		return false
	}
	jobEnv, ok := environmentGitOpsJobEnvironment(job.EnvOverrides)
	if !ok || !environmentGitOpsJobEnvironmentMatches(run.EnvOverrides, jobEnv) ||
		!environmentGitOpsJobEnvironmentMatches(run.EffectiveEnvSnapshot, jobEnv) {
		return false
	}
	return true
}

func environmentGitOpsJobEnvironment(raw json.RawMessage) (map[string]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var values map[string]string
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, false
	}
	return values, true
}

func environmentGitOpsJobEnvironmentMatches(raw json.RawMessage, expected map[string]string) bool {
	actual, ok := environmentGitOpsJobEnvironment(raw)
	if !ok {
		return false
	}
	if expected == nil {
		expected = map[string]string{}
	}
	return maps.Equal(actual, expected)
}

func environmentGitOpsScheduledJobMembers(graph EnvironmentWorkloadGraph) []EnvironmentWorkloadServingScheduledJob {
	jobs := make([]EnvironmentWorkloadServingScheduledJob, 0)
	for _, member := range graph.Members {
		if member.ExecutionMode == api.ExecutionModeJob && member.ScheduleConfigured {
			jobs = append(jobs, EnvironmentWorkloadServingScheduledJob{AppID: member.AppID, JobID: member.JobID,
				ServiceBindings: maps.Clone(member.ServiceBindings)})
		}
	}
	return jobs
}

func (m *MemStore) environmentGitOpsScheduledJobsReadyLocked(memory *environmentGitOpsMemory,
	graph EnvironmentWorkloadGraph, status string) bool {
	for _, member := range graph.Members {
		if member.ExecutionMode != api.ExecutionModeJob || !member.ScheduleConfigured {
			continue
		}
		intent, exists := m.appEnvironmentWorkloadIntents[environmentWorkloadIntentKey{AppID: member.AppID, EnvironmentID: graph.EnvironmentID}]
		job, jobExists := m.jobs[member.JobID]
		if !exists || !jobExists || !environmentGitOpsScheduledJobMatches(graph, member, intent, job, status) {
			return false
		}
	}
	return true
}

func (m *MemStore) environmentWorkloadScheduledJobAcksLocked(graph EnvironmentWorkloadGraph, release ProjectReleaseSet,
	targets map[string]string) []EnvironmentWorkloadServingScheduledJobAck {
	acks := make([]EnvironmentWorkloadServingScheduledJobAck, 0)
	if release.ID == "" || release.CreatedAt.IsZero() || !release.Active {
		return acks
	}
	memory, exists := m.environmentGitOps[graph.SourceID]
	if !exists || !m.environmentGitOpsScheduledJobsReadyLocked(memory, graph, "active") {
		return acks
	}
	for _, member := range graph.Members {
		if member.ExecutionMode != api.ExecutionModeJob || !member.ScheduleConfigured {
			continue
		}
		deploymentID, ok := environmentGraphMemberServingDeployment(member, targets)
		if !ok || releaseMemberForApp(release, member.AppID) != deploymentID {
			continue
		}
		job := m.jobs[member.JobID]
		var best *EnvironmentWorkloadServingScheduledJobAck
		for _, occurrence := range m.scheduleOccurrences {
			if occurrence.JobID != job.ID || occurrence.ScheduleRevision != job.ScheduleRevision || occurrence.Status != "succeeded" ||
				occurrence.JobRunID == "" || occurrence.FinishedAt == nil || occurrence.FinishedAt.Before(release.CreatedAt) || occurrence.CreatedAt.Before(release.CreatedAt) {
				continue
			}
			run, runExists := m.jobRuns[occurrence.JobRunID]
			if !runExists || run.ID != occurrence.JobRunID || !environmentGitOpsScheduledJobRunMatches(job, occurrence, run, release.CreatedAt) {
				continue
			}
			candidate := EnvironmentWorkloadServingScheduledJobAck{AppID: member.AppID, JobID: job.ID, JobRunID: run.ID,
				OccurrenceID: occurrence.ID, AcknowledgedAt: *occurrence.FinishedAt, ServiceBindings: maps.Clone(member.ServiceBindings)}
			if best == nil || candidate.AcknowledgedAt.Before(best.AcknowledgedAt) ||
				candidate.AcknowledgedAt.Equal(best.AcknowledgedAt) && candidate.JobRunID < best.JobRunID {
				copy := candidate
				best = &copy
			}
		}
		if best != nil {
			acks = append(acks, *best)
		}
	}
	return acks
}
