package state

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func jobDefinitionSQLParams(job Job) (sqlc.CreateJobDefinitionWithComputeRegionParams, error) {
	params := sqlc.CreateJobDefinitionWithComputeRegionParams{
		AccountID: uuidToPgtype(job.AccountID), Kind: job.Kind, Name: job.Name, ImageRef: job.ImageRef,
		RamMb: int32(job.RAMMB), TaskTimeoutS: int32(job.TaskTimeoutS), MaxParallelism: int32(job.MaxParallelism), RetryMax: int32(job.RetryMax),
		EnvOverrides: job.EnvOverrides, Command: job.Command, CronSchedule: job.CronSchedule, CronTimezone: job.CronTimezone, ComputeRegion: job.ComputeRegion,
	}
	var err error
	if job.SchedulePolicy != nil {
		params.SchedulePolicy, err = json.Marshal(job.SchedulePolicy)
		if err != nil {
			return params, fmt.Errorf("state: encode job schedule policy: %w", err)
		}
	}
	if job.FailureRules != nil {
		params.FailureRules, err = json.Marshal(job.FailureRules)
		if err != nil {
			return params, fmt.Errorf("state: encode job failure rules: %w", err)
		}
	}
	return params, nil
}

func jobDefinitionFromSQL(record sqlc.Job) (Job, error) {
	job := Job{
		ID: uuidString(record.ID), AccountID: uuidString(record.AccountID), Kind: record.Kind, Name: record.Name, ImageRef: record.ImageRef,
		RAMMB: int(record.RamMb), TaskTimeoutS: int(record.TaskTimeoutS), MaxParallelism: int(record.MaxParallelism), RetryMax: int(record.RetryMax),
		EnvOverrides: record.EnvOverrides, Command: record.Command, Status: record.Status, ComputeRegion: record.ComputeRegion,
		CronSchedule: record.CronSchedule.String, CronTimezone: record.CronTimezone, LastScheduledAt: timestamptzToTimePtr(record.LastScheduledAt),
		CreatedAt: record.CreatedAt.Time, UpdatedAt: record.UpdatedAt.Time, ScheduleRevision: record.ScheduleRevision,
		ImageResolvedDigest: record.ImageResolvedDigest.String, ImageStorageKey: record.ImageStorageKey.String,
		ImageMaterializationStatus: record.ImageMaterializationStatus, ImageMaterializationError: record.ImageMaterializationError.String,
		ImageMaterializedAt: timestamptzToTimePtr(record.ImageMaterializedAt), ImageMaterializationAttempts: int(record.ImageMaterializationAttempts),
		ImageMaterializationNextAttemptAt: timestamptzToTimePtr(record.ImageMaterializationNextAttemptAt),
	}
	if len(record.SchedulePolicy) > 0 {
		if err := json.Unmarshal(record.SchedulePolicy, &job.SchedulePolicy); err != nil {
			return Job{}, fmt.Errorf("state: decode job schedule policy: %w", err)
		}
	}
	if len(record.FailureRules) > 0 {
		if err := json.Unmarshal(record.FailureRules, &job.FailureRules); err != nil {
			return Job{}, fmt.Errorf("state: decode job failure rules: %w", err)
		}
	}
	return job, nil
}
