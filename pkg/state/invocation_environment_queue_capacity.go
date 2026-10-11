package state

import "github.com/onebox-faas/faas/pkg/api"

// The existing per-app queue limit is independently enforced for each stage
// workload. All named queues and deployment generations share that domain.
func environmentQueueProducerCapacity(plan api.Plan, depth int64) error {
	limits, valid := api.LimitsFor(plan)
	if !valid {
		return ErrInvalidArgument
	}
	if limits.MaxQueueDepth <= 0 || depth >= int64(limits.MaxQueueDepth) {
		return ErrQuotaExceeded
	}
	return nil
}

func (m *MemStore) environmentQueueProducerDepthLocked(environmentID, appID string) int64 {
	var depth int64
	for id, inv := range m.invocations {
		if inv.State != InvocationPending && inv.State != InvocationDispatching && !inv.QuotaReserved {
			continue
		}
		owner, owned := m.invocationEnvironmentQueueAdmissions[id]
		if (owned && owner.EnvironmentID == environmentID && owner.AppID == appID) ||
			(inv.EnvironmentID == environmentID && inv.AppID == appID && inv.Source == InvocationQueue) {
			depth++
		}
	}
	return depth
}
