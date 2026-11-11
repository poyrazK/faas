// adr: 20261109172421

package sched

import (
	"github.com/prometheus/client_golang/prometheus"
)

// CPU admission gates, the `gate` label of
// schedd_cpu_admission_would_refuse_total.
const (
	// CPUGatePlacement: no node had vCPU or CPU room; placement chose the
	// one that fits in memory.
	CPUGatePlacement = "placement"
	// CPUGateLedgerVCPU and CPUGateLedgerCPU: the ledger's per-node vCPU
	// and physical CPU headroom checks.
	CPUGateLedgerVCPU = "ledger_vcpu"
	CPUGateLedgerCPU  = "ledger_cpu"
)

// cpuAdmission counts what the per-node vCPU and CPU budgets would have
// refused. Memory is the only capacity limit (ADR-20261109172421): placement
// still prefers a node with CPU room, but neither it, the ledger nor the
// durable node reservation refuses work for CPU.
type cpuAdmission struct {
	wouldRefuse *prometheus.CounterVec
}

func newCPUAdmission() *cpuAdmission {
	c := &cpuAdmission{wouldRefuse: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "schedd_cpu_admission_would_refuse_total",
		Help: "Admissions past a node's vCPU or CPU budget; CPU never refuses work, so they were admitted on memory (ADR-20261109172421).",
	}, []string{"gate"})}
	for _, gate := range []string{CPUGatePlacement, CPUGateLedgerVCPU, CPUGateLedgerCPU} {
		c.wouldRefuse.WithLabelValues(gate)
	}
	return c
}

func (c *cpuAdmission) refusedAt(gate string) {
	if c != nil {
		c.wouldRefuse.WithLabelValues(gate).Inc()
	}
}

// WithCPUAdmissionMetrics registers schedd_cpu_admission_would_refuse_total.
func (e *Engine) WithCPUAdmissionMetrics(reg prometheus.Registerer) *Engine {
	if e != nil && e.ledger != nil && e.ledger.cpuAdmission != nil && reg != nil {
		reg.MustRegister(e.ledger.cpuAdmission.wouldRefuse)
	}
	return e
}
