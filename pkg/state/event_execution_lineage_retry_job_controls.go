package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func controlLineageRetryJob(job *api.EventExecutionLineageRetryJob, action string, expected time.Time) (bool, error) {
	if action == "cancel" {
		return cancelLineageRetryJob(job, expected)
	}
	if action != "pause" && action != "resume" {
		return false, ErrInvalidArgument
	}
	if !job.CreatedAt.Equal(expected) {
		return false, ErrConflict
	}
	if job.Status == "completed" || job.Status == "cancelled" {
		return false, ErrConflict
	}
	if (job.Status != "pending" && job.Status != "paused") || job.Processed < 0 || job.Processed >= len(job.Items) || len(job.Items) > api.EventExecutionLineageRetryJobMaxCases {
		return false, ErrInvalidArgument
	}
	for _, item := range job.Items[job.Processed:] {
		if item.Status != "pending" || item.Result != nil {
			return false, ErrInvalidArgument
		}
	}
	desired := "paused"
	if action == "resume" {
		desired = "pending"
	}
	if job.Status == desired {
		return false, nil
	}
	job.Status, job.UpdatedAt = desired, time.Now().UTC()
	return true, nil
}

func (s *PgStore) CancelEventExecutionLineageRetryJob(ctx context.Context, account, app, sub, id string, req api.CancelEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error) {
	return s.controlEventExecutionLineageRetryJob(ctx, account, app, sub, id, "cancel", api.ControlEventExecutionLineageRetryJobRequest(req))
}
func (m *MemStore) CancelEventExecutionLineageRetryJob(ctx context.Context, account, app, sub, id string, req api.CancelEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error) {
	return m.controlEventExecutionLineageRetryJob(ctx, account, app, sub, id, "cancel", api.ControlEventExecutionLineageRetryJobRequest(req))
}
func (s *PgStore) ControlEventExecutionLineageRetryJob(ctx context.Context, account, app, sub, id, action string, req api.ControlEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error) {
	if action != "pause" && action != "resume" {
		return api.EventExecutionLineageRetryJob{}, ErrInvalidArgument
	}
	return s.controlEventExecutionLineageRetryJob(ctx, account, app, sub, id, action, req)
}
func (m *MemStore) ControlEventExecutionLineageRetryJob(ctx context.Context, account, app, sub, id, action string, req api.ControlEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error) {
	if action != "pause" && action != "resume" {
		return api.EventExecutionLineageRetryJob{}, ErrInvalidArgument
	}
	return m.controlEventExecutionLineageRetryJob(ctx, account, app, sub, id, action, req)
}
