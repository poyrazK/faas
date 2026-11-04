package state

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func (m *MemStore) environmentGitOpsManagedJobLocked(jobID string) bool {
	if job, exists := m.jobs[jobID]; exists && job.EnvironmentGitSourceID != "" {
		return !m.environmentGitOpsJobReleasedLocked(job)
	}
	for _, intent := range m.appEnvironmentWorkloadIntents {
		if intent.JobID == jobID {
			return !m.environmentGitOpsJobReleasedLocked(m.jobs[jobID])
		}
	}
	return false
}

func (m *MemStore) environmentGitOpsJobReleasedLocked(job Job) bool {
	if job.EnvironmentGitSourceID != "" {
		source, known := m.environmentGitOps[job.EnvironmentGitSourceID]
		return known && source.source.Detached
	}
	// N-1 rows may already have been detached before attribution was added.
	// Release only a linked Job that predates a proven retired scope, with no
	// active replacement whose legacy ownership could be mistaken for history.
	if job.CreatedAt.IsZero() {
		return false
	}
	for _, intent := range m.appEnvironmentWorkloadIntents {
		if intent.JobID != job.ID || intent.AccountID != job.AccountID {
			continue
		}
		active, retired := false, false
		for _, memory := range m.environmentGitOps {
			if memory.source.AccountID != intent.AccountID || memory.source.EnvironmentID != intent.EnvironmentID {
				continue
			}
			active = active || !memory.source.Detached
			for _, appID := range memory.resources {
				retired = retired || memory.source.Detached && appID == intent.AppID && !job.CreatedAt.After(memory.source.UpdatedAt)
			}
		}
		if retired && !active {
			return true
		}
	}
	return false
}

func (m *MemStore) syncEnvironmentGitOpsJobLocked(source EnvironmentGitSource, row EnvironmentWorkloadIntent, app gitOpsIntentApp,
	workload api.EnvironmentWorkload, plan api.Plan, attributeSource bool) (EnvironmentWorkloadIntent, error) {
	// The caller passes the intent row after applying the reviewed plan. Its
	// variables are the approved snapshot; comparing them to the pre-apply app
	// observation would silently preserve stale Job environment values.
	if row.Variables == nil {
		row.Variables = map[string]string{}
	}
	envOverrides, err := json.Marshal(row.Variables)
	if err != nil {
		return row, ErrInvalidArgument
	}
	if row.Schedule == nil {
		if row.JobID == "" {
			return row, nil
		}
		job, exists := m.jobs[row.JobID]
		if !exists || job.AccountID != row.AccountID {
			return row, ErrConflict
		}
		job.Status, job.Kind, job.CronSchedule = "deleted", "batch", ""
		job.CronTimezone = "UTC"
		job.SchedulePolicy, job.FailureRules = nil, nil
		m.jobs[job.ID] = job
		row.JobID = ""
		return row, nil
	}
	if reason := environmentGitOpsScheduledJobUnsupported(app, row, workload, plan); reason != "" {
		return row, fmt.Errorf("%w: %s", ErrConflict, reason)
	}
	image := row.Source
	if image == nil {
		image, _, _ = observedWorkloadSource(app, EnvironmentGitSourceSpec{})
	}
	if image == nil {
		return row, ErrConflict
	}
	jobIntent := row
	jobIntent.Source = image
	sourceRef, ok := environmentGitOpsJobSourceRef(jobIntent)
	if !ok {
		return row, ErrConflict
	}
	command, commandOK := EnvironmentGitOpsJobCommand(jobIntent)
	if !commandOK {
		return row, ErrConflict
	}
	index := plan.PlanIndex()
	if !plan.JobsAllowed() || api.JobMaxPerAccount[index] <= 0 || api.JobRAMMB[index] <= 0 || api.JobTaskTimeoutSec[index] <= 0 || api.JobMaxParallelismPerRun[index] <= 0 {
		return row, ErrConflict
	}
	jobName := environmentGitOpsJobName(row.EnvironmentID, row.AppID)
	if prior, exists := m.jobs[row.JobID]; exists && prior.Status == "deleted" && !m.environmentGitOpsManagedJobLocked(prior.ID) {
		row.JobID = ""
	}
	if row.JobID == "" {
		count := 0
		for _, job := range m.jobs {
			if job.AccountID == row.AccountID && job.Status != "deleted" {
				count++
			}
		}
		limit := api.JobMaxPerAccount[index]
		if count >= limit {
			return row, &JobQuotaError{Scope: JobQuotaScopePerAccount, Limit: limit, Observed: count}
		}
		job, err := m.jobCreateLocked(row.AccountID, jobName, "recurring", sourceRef, command, api.JobRAMMB[index],
			api.JobTaskTimeoutSec[index], 1, 0, envOverrides)
		if err != nil {
			return row, err
		}
		job.Status = "paused"
		if attributeSource {
			job.EnvironmentGitSourceID = source.ID
		}
		job.CronSchedule, job.CronTimezone = row.Schedule.Cron, row.Schedule.Timezone
		job.SchedulePolicy = workpolicy.Clone(row.Schedule.SchedulePolicy)
		job.FailureRules = workpolicy.Clone(row.Schedule.FailureRules)
		m.jobs[job.ID] = job
		row.JobID = job.ID
		return row, nil
	}
	job, exists := m.jobs[row.JobID]
	if !exists || job.AccountID != row.AccountID || job.Status == "deleted" {
		return row, ErrConflict
	}
	for _, tasks := range m.jobTasks {
		for _, task := range tasks {
			if task.Status != "queued" && task.Status != "claimed" {
				continue
			}
			if run, ok := m.jobRuns[task.RunID]; ok && run.JobID == job.ID {
				return row, ErrConflict
			}
		}
	}
	if job.ImageRef != sourceRef {
		job.ImageResolvedDigest, job.ImageStorageKey = "", ""
		job.ImageMaterializationStatus, job.ImageMaterializationError = "pending", ""
		job.ImageMaterializedAt, job.ImageMaterializationNextAttemptAt = nil, nil
		job.ImageMaterializationAttempts = 0
	}
	job.Name, job.Kind, job.ImageRef = jobName, "recurring", sourceRef
	job.RAMMB, job.TaskTimeoutS, job.MaxParallelism, job.RetryMax = api.JobRAMMB[index], api.JobTaskTimeoutSec[index], 1, 0
	job.EnvOverrides, job.Status, job.Command = append(json.RawMessage(nil), envOverrides...), "paused", command
	if attributeSource {
		job.EnvironmentGitSourceID = source.ID
	}
	job.CronSchedule, job.CronTimezone = row.Schedule.Cron, row.Schedule.Timezone
	job.SchedulePolicy = workpolicy.Clone(row.Schedule.SchedulePolicy)
	job.FailureRules = workpolicy.Clone(row.Schedule.FailureRules)
	job.UpdatedAt = time.Now().UTC()
	m.jobs[job.ID] = job
	return row, nil
}
