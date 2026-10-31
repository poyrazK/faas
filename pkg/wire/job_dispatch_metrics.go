package wire

import "github.com/prometheus/client_golang/prometheus"

// Job dispatch outcomes, one per ready task the dispatch tick decided on.
// Customer limits (account_capacity, quota, capability, spend_cap, budget,
// account_inactive) and benign races (not_ready) are expected; node_capacity,
// compute_unavailable and wake_error mean the platform could not start
// ready work.
const (
	JobDispatchStarted            = "started"
	JobDispatchAccountCapacity    = "account_capacity"
	JobDispatchQuota              = "quota"
	JobDispatchCapability         = "capability"
	JobDispatchSpendCap           = "spend_cap"
	JobDispatchBudget             = "budget"
	JobDispatchAccountInactive    = "account_inactive"
	JobDispatchNotReady           = "not_ready"
	JobDispatchNodeCapacity       = "node_capacity"
	JobDispatchComputeUnavailable = "compute_unavailable"
	JobDispatchWakeError          = "wake_error"
)

var jobDispatchResults = []string{
	JobDispatchStarted, JobDispatchAccountCapacity, JobDispatchQuota, JobDispatchCapability,
	JobDispatchSpendCap, JobDispatchBudget, JobDispatchAccountInactive, JobDispatchNotReady,
	JobDispatchNodeCapacity, JobDispatchComputeUnavailable, JobDispatchWakeError,
}

// JobDispatchMetrics counts schedd's job dispatch decisions. The result label
// is a closed vocabulary; it never carries account, job or run IDs.
type JobDispatchMetrics struct {
	decisions *prometheus.CounterVec
}

func NewJobDispatchMetrics(reg prometheus.Registerer, prefix string) *JobDispatchMetrics {
	metrics := &JobDispatchMetrics{
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_job_dispatch_total",
			Help: "Ready job tasks the dispatch tick started or held, by result.",
		}, []string{"result"}),
	}
	for _, result := range jobDispatchResults {
		metrics.decisions.WithLabelValues(result)
	}
	reg.MustRegister(metrics.decisions)
	return metrics
}

// Observe counts one dispatch decision. An unknown result counts as wake_error.
func (m *JobDispatchMetrics) Observe(result string) {
	if m == nil {
		return
	}
	known := false
	for _, candidate := range jobDispatchResults {
		known = known || candidate == result
	}
	if !known {
		result = JobDispatchWakeError
	}
	m.decisions.WithLabelValues(result).Inc()
}
