package state

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cloneWorkerRecoveryPools(pools []WorkerRecoveryPool) []WorkerRecoveryPool {
	out := slices.Clone(pools)
	for i := range out {
		out[i].Blockers = slices.Clone(out[i].Blockers)
		for j := range out[i].Blockers {
			out[i].Blockers[j] = out[i].Blockers[j].Clone()
		}
	}
	return out
}

func validWorkerAdmissionBlockers(blockers []api.WorkerAdmissionBlocker, observedAt time.Time) bool {
	if len(blockers) > api.WorkerAdmissionBlockersMax {
		return false
	}
	seen := make(map[string]bool, len(blockers))
	for _, b := range blockers {
		if b.ObservedAt.IsZero() || b.ObservedAt.After(observedAt) || seen[b.Code] {
			return false
		}
		seen[b.Code] = true
		switch b.Code {
		case "account_worker_limit", "concurrency_limit", "node_ram", "node_vcpu", "node_cpu", "no_admitting_nodes", "node_ports",
			"placement_changed", "spend_cap", "retry_cooldown", "admission_refused", "deployment_unavailable":
		default:
			return false
		}
		switch b.Resource {
		case "", "replicas", "ram_mb", "vcpu", "cpu_millicores", "cents":
		default:
			return false
		}
		if b.Requested != nil && *b.Requested < 0 || b.Used != nil && *b.Used < 0 || b.Limit != nil && *b.Limit < 0 {
			return false
		}
		if (b.Used == nil) != (b.Limit == nil) || b.Resource == "" && (b.Used != nil || b.Requested != nil) {
			return false
		}
		if b.RetryAt != nil && (b.Code != "retry_cooldown" || !b.RetryAt.After(b.ObservedAt)) {
			return false
		}
		if b.Code == "retry_cooldown" && b.RetryAt == nil {
			return false
		}
	}
	return true
}
