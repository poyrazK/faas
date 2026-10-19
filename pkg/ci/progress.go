package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProgressUpdate is a single serialized append. Retrying the latest sequence
// requires the same canonical payload. Historical or conflicting writes fail.
type ProgressUpdate struct {
	Sequence     int32        `json:"sequence"`
	LogOffset    int          `json:"log_offset"`
	Logs         []byte       `json:"logs"`
	LogTruncated bool         `json:"log_truncated"`
	Steps        []StepResult `json:"steps"`
}

type ProgressReceipt struct {
	Sequence   int32 `json:"sequence"`
	NextOffset int   `json:"next_offset"`
}

// JobsProgress is one bounded retained prefix, independent of sealed output.
// It may contain completed steps, but can never certify a stopped native task.
type JobsProgress struct {
	Steps        []StepResult `json:"steps"`
	Logs         []byte       `json:"logs"`
	LogTruncated bool         `json:"log_truncated"`
}

type RuntimeProgressStore interface {
	AppendJobsRuntimeProgress(context.Context, Config, RuntimeProof, ProgressUpdate) (ProgressReceipt, error)
}

func DecodeProgressUpdate(raw []byte) (ProgressUpdate, error) {
	var update ProgressUpdate
	err := decodeRuntimeJSON(raw, &update, api.CIJobsProgressRequestMaxBytes)
	return update, err
}

func initialJobsProgress(job PlannedJob) JobsProgress {
	out := JobsProgress{Steps: make([]StepResult, len(job.Steps))}
	for i, step := range job.Steps {
		out.Steps[i] = StepResult{Name: step.Name, Status: StatusQueued}
	}
	return out
}

func decodeJobsProgress(raw []byte, job PlannedJob) (JobsProgress, error) {
	out := initialJobsProgress(job)
	if len(raw) == 0 {
		return out, nil
	}
	if len(raw) > api.CIJobsProgressSnapshotMaxBytes || json.Unmarshal(raw, &out) != nil || len(out.Steps) != len(job.Steps) || len(out.Logs) > api.CILogMaxBytesPerAttempt || out.LogTruncated && len(out.Logs) != api.CILogMaxBytesPerAttempt {
		return JobsProgress{}, ErrBackendProtocol
	}
	if err := validateSteps(initialJobsProgress(job).Steps, Observation{Status: StatusRunning, Steps: out.Steps}); err != nil {
		return JobsProgress{}, err
	}
	return out, nil
}

func appendJobsProgress(job PlannedJob, before JobsProgress, update ProgressUpdate) (JobsProgress, error) {
	if update.Sequence < 1 || update.Sequence > api.CIJobsProgressMaxUpdates || update.LogOffset != len(before.Logs) || len(update.Logs) > api.CILogPageMaxBytes || len(update.Logs) > api.CILogMaxBytesPerAttempt-len(before.Logs) {
		return JobsProgress{}, ErrBackendProtocol
	}
	if update.LogTruncated && len(before.Logs)+len(update.Logs) != api.CILogMaxBytesPerAttempt || before.LogTruncated && !update.LogTruncated {
		return JobsProgress{}, ErrBackendProtocol
	}
	if len(update.Steps) != len(job.Steps) {
		return JobsProgress{}, ErrBackendProtocol
	}
	if err := validateSteps(before.Steps, Observation{Status: StatusRunning, Steps: update.Steps}); err != nil {
		return JobsProgress{}, err
	}
	if len(update.Logs) == 0 && before.LogTruncated == update.LogTruncated && reflect.DeepEqual(before.Steps, update.Steps) {
		return JobsProgress{}, ErrConflict
	}
	out := JobsProgress{Steps: clone(update.Steps), Logs: append(bytes.Clone(before.Logs), update.Logs...), LogTruncated: update.LogTruncated}
	return out, nil
}

func progressPage(progress JobsProgress, req ObserveRequest) (Observation, error) {
	return jobsLogPage(Observation{Status: StatusRunning, Steps: clone(progress.Steps), Logs: bytes.Clone(progress.Logs), MoreLogs: progress.LogTruncated}, req)
}

func reconcileProgressOutput(progress *JobsProgress, output Observation) (Observation, error) {
	if progress == nil {
		return output, nil
	}
	if !bytes.HasPrefix(output.Logs, progress.Logs) || progress.LogTruncated && !output.MoreLogs {
		return Observation{}, protocolError("sealed logs changed the live prefix")
	}
	if err := validateSteps(progress.Steps, output); err != nil {
		return Observation{}, err
	}
	return output, nil
}

func incompleteProgressOutput(job PlannedJob, task jobsTask, progress *JobsProgress, now time.Time) Observation {
	out := incompleteRuntimeOutput(job, task)
	out.OutputIncomplete = true
	if progress == nil {
		return out
	}
	note := out.Logs
	out.Steps = clone(progress.Steps)
	for i := range out.Steps {
		step := &out.Steps[i]
		if !terminal(step.Status) {
			step.Status = StatusCancelled
			if step.StartedAt != nil {
				at := now
				if at.Before(*step.StartedAt) {
					at = *step.StartedAt
				}
				latest := step.StartedAt.Add(time.Duration(api.CIMaxTimeoutSeconds+api.CIJobsFinalizeSeconds) * time.Second)
				if at.After(latest) {
					at = latest
				}
				step.FinishedAt = &at
			}
		}
	}
	keep := min(len(note), api.CILogMaxBytesPerAttempt-len(progress.Logs))
	out.Logs = append(bytes.Clone(progress.Logs), note[:keep]...)
	out.MoreLogs = progress.LogTruncated || keep < len(note)
	return out
}
