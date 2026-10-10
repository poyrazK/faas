package state

// ADR-958 interactive-session records (see app_task_attach.go).

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

var _ AppTaskAttachStore = (*PgStore)(nil)

func (s *PgStore) AppTaskAttachByTask(ctx context.Context, taskID string) (AppTaskAttach, error) {
	var record AppTaskAttach
	var nodeID pgtype.Text
	var nodeRecordedAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		select task_id::text, tty, attach_token_sha256, node_id, node_recorded_at, created_at
		  from app_task_attach_sessions where task_id::text = $1`, taskID).
		Scan(&record.TaskID, &record.TTY, &record.TokenSHA256, &nodeID, &nodeRecordedAt, &record.CreatedAt)
	if err != nil {
		return AppTaskAttach{}, mapErr(err)
	}
	record.NodeID = nodeID.String
	record.NodeRecordedAt = timestamptzToTimePtr(nodeRecordedAt)
	record.CreatedAt = record.CreatedAt.UTC()
	return record, nil
}

func (s *PgStore) RecordAppTaskAttachNode(ctx context.Context, taskID, leaseToken, nodeID string, recordedAt time.Time) error {
	if taskID == "" || leaseToken == "" || !validAppTaskAttachNodeID(nodeID) || recordedAt.IsZero() {
		return ErrAppTaskInvalid
	}
	tag, err := s.pool.Exec(ctx, `
		update app_task_attach_sessions session
		   set node_id = $3, node_recorded_at = $4
		  from app_tasks task
		 where session.task_id = $1::uuid and task.id = session.task_id
		   and task.status = 'running' and task.lease_token = $2::uuid`,
		taskID, leaseToken, nodeID, recordedAt.UTC())
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAppTaskLeaseLost
	}
	return nil
}

func (s *PgStore) AppTaskAttachTarget(ctx context.Context, accountID, appID, taskID string) (AppTaskAttachTarget, error) {
	var target AppTaskAttachTarget
	var status string
	var nodeID pgtype.Text
	err := s.pool.QueryRow(ctx, `
		select task.id::text, task.status, session.tty, session.node_id
		  from app_tasks task
		  join app_task_attach_sessions session on session.task_id = task.id
		 where task.id::text = $3 and task.account_id::text = $1 and task.app_id::text = $2`,
		accountID, appID, taskID).Scan(&target.TaskID, &status, &target.TTY, &nodeID)
	if err != nil {
		return AppTaskAttachTarget{}, mapErr(err)
	}
	target.TaskStatus = AppTaskStatus(status)
	target.NodeID = nodeID.String
	return target, nil
}
