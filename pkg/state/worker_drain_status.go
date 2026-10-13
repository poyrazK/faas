package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DeploymentWorkerDrainStatus counts instance shutdowns, not application task
// executions. Overdue marks an unfinished audit span; it does not claim that
// an unreachable workload has exited.
type DeploymentWorkerDrainStatus struct {
	Active    int        `json:"active"`
	Completed int        `json:"completed"`
	TimedOut  int        `json:"timed_out"`
	Failed    int        `json:"failed"`
	Overdue   int        `json:"overdue"`
	Deadline  *time.Time `json:"deadline,omitempty"`
}

type workerDrainAuditData struct {
	InstanceID string    `json:"instance_id"`
	Deadline   time.Time `json:"deadline"`
	Outcome    string    `json:"outcome"`
}

func summarizeWorkerDrains(records []json.RawMessage, now time.Time) *DeploymentWorkerDrainStatus {
	var result DeploymentWorkerDrainStatus
	seen := make(map[string]bool)
	for _, raw := range records {
		var record workerDrainAuditData
		if json.Unmarshal(raw, &record) != nil || record.InstanceID == "" || seen[record.InstanceID] {
			continue
		}
		seen[record.InstanceID] = true
		switch record.Outcome {
		case "completed":
			result.Completed++
		case "timed_out":
			result.TimedOut++
		case "failed":
			result.Failed++
		case "draining":
			result.Active++
			if !record.Deadline.IsZero() {
				if !record.Deadline.After(now) {
					result.Overdue++
				}
				if result.Deadline == nil || record.Deadline.Before(*result.Deadline) {
					deadline := record.Deadline
					result.Deadline = &deadline
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	return &result
}

func (s *PgStore) DeploymentWorkerDrainStatus(ctx context.Context, id string) (*DeploymentWorkerDrainStatus, error) {
	rows, err := s.pool.Query(ctx, `SELECT data FROM (
        SELECT DISTINCT ON (data->>'instance_id') data,id FROM deployment_audit
        WHERE deployment_id=$1 AND kind IN ('deploy.worker_drain_started','deploy.worker_drain_completed')
        ORDER BY data->>'instance_id',id DESC
    ) latest ORDER BY id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []json.RawMessage
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		records = append(records, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summarizeWorkerDrains(records, time.Now().UTC()), nil
}

func (m *MemStore) DeploymentWorkerDrainStatus(ctx context.Context, id string) (*DeploymentWorkerDrainStatus, error) {
	want, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("state: worker drain deployment ID: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var records []json.RawMessage
	for i := len(m.deploymentAudit) - 1; i >= 0; i-- {
		a := m.deploymentAudit[i]
		if a.DeploymentID == want && (a.Kind == DeployWorkerDrainStarted || a.Kind == DeployWorkerDrainCompleted) {
			records = append(records, a.Data)
		}
	}
	return summarizeWorkerDrains(records, time.Now().UTC()), nil
}
