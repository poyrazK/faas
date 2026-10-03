package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// JobExecutionInstance is the persisted instance ownership used at the job
// boot boundary. The durable run/task link lives in job_tasks.instance_id.
type JobExecutionInstance struct {
	ID, JobID, NodeID, Kind, State string
	RAMMB                          int
}

func (s *PgStore) JobExecutionInstanceByID(ctx context.Context, id string) (JobExecutionInstance, error) {
	row, err := sqlc.New().GetJobExecutionInstance(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return JobExecutionInstance{}, mapErr(err)
	}
	return JobExecutionInstance{
		ID: uuidString(row.ID), JobID: uuidString(row.JobID), NodeID: uuidString(row.NodeID),
		Kind: row.Kind, State: row.State, RAMMB: int(row.RamMb),
	}, nil
}

func (m *MemStore) JobExecutionInstanceByID(ctx context.Context, id string) (JobExecutionInstance, error) {
	inst, err := m.InstanceByID(ctx, id)
	if err != nil {
		return JobExecutionInstance{}, err
	}
	return JobExecutionInstance{
		ID: inst.ID, JobID: inst.JobID, NodeID: inst.NodeID,
		Kind: inst.Kind, State: inst.State, RAMMB: inst.RAMMB,
	}, nil
}
