package state

import (
	"context"
	"sort"

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

// JobExecutionInstances retains terminal rows for placement auditing. The
// billing sampler's ListJobInstances deliberately returns only live rows.
func (s *PgStore) JobExecutionInstances(ctx context.Context, jobID string) ([]JobExecutionInstance, error) {
	rows, err := sqlc.New().ListJobExecutionInstances(ctx, s.pool, mustPgUUID(jobID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]JobExecutionInstance, 0, len(rows))
	for _, row := range rows {
		out = append(out, JobExecutionInstance{
			ID: uuidString(row.ID), JobID: uuidString(row.JobID), NodeID: uuidString(row.NodeID),
			Kind: row.Kind, State: row.State, RAMMB: int(row.RamMb),
		})
	}
	return out, nil
}

func (m *MemStore) JobExecutionInstances(_ context.Context, jobID string) ([]JobExecutionInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]JobExecutionInstance, 0)
	for _, inst := range m.instances {
		if inst.JobID == jobID {
			out = append(out, JobExecutionInstance{ID: inst.ID, JobID: inst.JobID, NodeID: inst.NodeID, Kind: inst.Kind, State: inst.State, RAMMB: inst.RAMMB})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
