// adr: 20261022155812
package state

import (
	"context"
	"sort"
	"strconv"
	"time"
)

var _ EnvironmentQualificationPullSmokeStore = (*MemStore)(nil)

type memQualificationPullSmoke struct {
	smoke          EnvironmentQualificationPullSmoke
	receiptSHA256  string
	leaseExpiresAt time.Time
}

func qualificationPullSmokeKey(requestID string, attempt int64, instanceID, binding string) string {
	return requestID + "/" + strconv.FormatInt(attempt, 10) + "/" + instanceID + "/" + binding
}

func (m *MemStore) ArmEnvironmentQualificationPullSmoke(ctx context.Context, smoke EnvironmentQualificationPullSmoke) error {
	if !qualificationPullSmokeArmValid(smoke) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	memory, request, err := m.qualificationLocked(smoke.RequestID)
	if err != nil || request.Attempt != smoke.Attempt || m.qualificationCurrentLocked(memory, request) != nil || request.LeaseUntil == nil {
		return ErrConflict
	}
	status, executing := m.qualificationExecutions[smoke.InstanceID]
	if !executing || status.Execution.RequestID != request.ID || !qualificationServiceExecutionCurrent(request, status, m.instances[smoke.InstanceID], now) {
		return ErrConflict
	}
	queue, payload, err := qualificationPullSmokeFrozen(request, smoke.Binding)
	if err != nil {
		return err
	}
	deadline := smoke.Deadline
	if request.LeaseUntil.Before(deadline) {
		deadline = *request.LeaseUntil
	}
	if !now.Before(deadline) {
		return ErrConflict
	}
	key := qualificationPullSmokeKey(smoke.RequestID, smoke.Attempt, smoke.InstanceID, smoke.Binding)
	if existing, armed := m.qualificationPullSmokes[key]; armed {
		if existing.smoke.MessageID != smoke.MessageID || existing.smoke.Outcome != "" {
			return ErrConflict
		}
		return nil
	}
	if m.qualificationPullSmokes == nil {
		m.qualificationPullSmokes = map[string]*memQualificationPullSmoke{}
	}
	m.qualificationPullSmokes[key] = &memQualificationPullSmoke{smoke: EnvironmentQualificationPullSmoke{
		RequestID: smoke.RequestID, Attempt: smoke.Attempt, InstanceID: smoke.InstanceID, Binding: smoke.Binding,
		QueueName: queue, MessageID: smoke.MessageID, Payload: payload, Deadline: deadline, ArmedAt: now,
	}}
	return nil
}

func (m *MemStore) EnvironmentQualificationPullSmoke(ctx context.Context, requestID string, attempt int64, instanceID, binding string) (EnvironmentQualificationPullSmoke, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationPullSmoke{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, armed := m.qualificationPullSmokes[qualificationPullSmokeKey(requestID, attempt, instanceID, binding)]
	if !armed {
		return EnvironmentQualificationPullSmoke{}, ErrNotFound
	}
	out := row.smoke
	out.Payload = append([]byte(nil), row.smoke.Payload...)
	return out, nil
}

// qualificationPullCallerLocked resolves the one current held candidate
// execution behind the observed node address, as the service route does.
func (m *MemStore) qualificationPullCallerLocked(caller EnvironmentQualificationPullCaller, now time.Time) (EnvironmentWorkloadQualificationRequest, Instance, error) {
	ins, err := m.qualificationNetworkInstanceLocked(caller.NodeID, caller.HostIP)
	if err != nil || ins.ID == "" || !m.deployments[ins.DeploymentID].EnvironmentWorkloadHeld() {
		return EnvironmentWorkloadQualificationRequest{}, Instance{}, ErrConflict
	}
	status := m.qualificationExecutions[ins.ID]
	memory, request, err := m.qualificationLocked(status.Execution.RequestID)
	if err != nil || m.qualificationCurrentLocked(memory, request) != nil || !qualificationServiceExecutionCurrent(request, status, ins, now) {
		return EnvironmentWorkloadQualificationRequest{}, Instance{}, ErrConflict
	}
	config, acknowledged := m.qualificationConfigReceipts[ins.ID]
	if !acknowledged || !qualificationConfigReceiptMatchesFrame(config, request, status.Execution) {
		return EnvironmentWorkloadQualificationRequest{}, Instance{}, ErrConflict
	}
	if m.apps[request.AppID].Slug != caller.App {
		return EnvironmentWorkloadQualificationRequest{}, Instance{}, ErrNotFound
	}
	return request, ins, nil
}

// qualificationPullRowsLocked lists the caller's armed rows for its queue in
// binding order.
func (m *MemStore) qualificationPullRowsLocked(request EnvironmentWorkloadQualificationRequest, instanceID, queue string) []*memQualificationPullSmoke {
	names := make([]string, 0, len(request.FrozenInputs.QueueBindings))
	for name := range request.FrozenInputs.QueueBindings {
		names = append(names, name)
	}
	sort.Strings(names)
	var rows []*memQualificationPullSmoke
	for _, name := range names {
		row, armed := m.qualificationPullSmokes[qualificationPullSmokeKey(request.ID, request.Attempt, instanceID, name)]
		if armed && row.smoke.QueueName == queue {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m *MemStore) LeaseEnvironmentQualificationPullSmoke(ctx context.Context, caller EnvironmentQualificationPullCaller, visibility time.Duration) ([]EnvironmentQualificationPullLease, error) {
	if !qualificationPullCallerValid(caller) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	request, ins, err := m.qualificationPullCallerLocked(caller, now)
	if err != nil {
		return nil, err
	}
	if !qualificationPullQueueFrozen(request, caller.Queue) {
		return nil, ErrNotFound
	}
	for _, row := range m.qualificationPullRowsLocked(request, ins.ID, caller.Queue) {
		if row.smoke.Outcome != "" || !now.Before(row.smoke.Deadline) || row.receiptSHA256 != "" && now.Before(row.leaseExpiresAt) {
			continue
		}
		until, ok := qualificationPullVisibility(visibility, now, row.smoke.Deadline)
		if !ok {
			return nil, ErrInvalidArgument
		}
		receipt, sha, err := newQualificationPullReceipt()
		if err != nil {
			return nil, err
		}
		row.receiptSHA256, row.leaseExpiresAt = sha, until
		row.smoke.Deliveries++
		return []EnvironmentQualificationPullLease{{MessageID: row.smoke.MessageID, Receipt: receipt,
			Payload: append([]byte(nil), row.smoke.Payload...), Attempt: row.smoke.Deliveries, ArmedAt: row.smoke.ArmedAt, LeaseExpiresAt: until}}, nil
	}
	return nil, nil
}

func (m *MemStore) SettleEnvironmentQualificationPullSmoke(ctx context.Context, caller EnvironmentQualificationPullCaller, verb string, receipts []string, visibility time.Duration) (QueueLeaseOutcome, error) {
	var out QueueLeaseOutcome
	if !qualificationPullCallerValid(caller) || !qualificationPullSettleValid(verb, receipts) {
		return out, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	request, ins, err := m.qualificationPullCallerLocked(caller, now)
	if err != nil {
		return out, err
	}
	if !qualificationPullQueueFrozen(request, caller.Queue) {
		return out, ErrNotFound
	}
	rows := m.qualificationPullRowsLocked(request, ins.ID, caller.Queue)
	for _, receipt := range receipts {
		var row *memQualificationPullSmoke
		if qualificationPullReceiptValid(receipt) {
			sha := qualificationPullReceiptSHA256(receipt)
			for _, candidate := range rows {
				if candidate.receiptSHA256 == sha {
					row = candidate
				}
			}
		}
		if row == nil {
			out.Failed = append(out.Failed, QueueLeaseFailure{Receipt: receipt, Reason: QueueLeaseReasonInvalid})
			continue
		}
		if row.smoke.Outcome != "" || !now.Before(row.leaseExpiresAt) || !now.Before(row.smoke.Deadline) {
			out.Failed = append(out.Failed, QueueLeaseFailure{Receipt: receipt, Reason: QueueLeaseReasonExpired})
			continue
		}
		switch verb {
		case PullSmokeExtend:
			until, ok := qualificationPullVisibility(visibility, now, row.smoke.Deadline)
			if !ok {
				return QueueLeaseOutcome{}, ErrInvalidArgument
			}
			row.leaseExpiresAt = until
		default:
			settled := now
			row.smoke.Outcome, row.smoke.SettledAt = PullSmokeAcked, &settled
			if verb == PullSmokeNack {
				row.smoke.Outcome = PullSmokeNacked
			}
		}
		out.Succeeded = append(out.Succeeded, receipt)
	}
	return out, nil
}

// qualificationPullQueueFrozen reports an enabled pull binding of the
// candidate on queue, so an unknown queue is a 404 like production leases.
func qualificationPullQueueFrozen(request EnvironmentWorkloadQualificationRequest, queue string) bool {
	for name := range request.FrozenInputs.QueueBindings {
		if frozenQueue, _, err := qualificationPullSmokeFrozen(request, name); err == nil && frozenQueue == queue {
			return true
		}
	}
	return false
}
