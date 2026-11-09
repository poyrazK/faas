// adr: 20261109172421

package sched

import (
	"fmt"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
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

// cpuAdmission is schedd's `cpu_admission` (ADR-20261109172421). Enforcing,
// the vCPU and CPU budgets refuse work as before. Observing, memory is the
// only capacity limit: placement still prefers a node with CPU room, the
// ledger and the durable node reservation stop checking vCPU and CPU, and
// each refusal they would have made is counted.
type cpuAdmission struct {
	observe     atomic.Bool
	wouldRefuse *prometheus.CounterVec
}

func newCPUAdmission() *cpuAdmission {
	c := &cpuAdmission{wouldRefuse: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "schedd_cpu_admission_would_refuse_total",
		Help: "Admissions the vCPU or CPU budget would have refused while cpu_admission is observe; they were admitted on memory (ADR-20261109172421).",
	}, []string{"gate"})}
	for _, gate := range []string{CPUGatePlacement, CPUGateLedgerVCPU, CPUGateLedgerCPU} {
		c.wouldRefuse.WithLabelValues(gate)
	}
	return c
}

// observing is true when CPU must not refuse work. A nil cpuAdmission
// enforces.
func (c *cpuAdmission) observing() bool {
	return c != nil && c.observe.Load()
}

func (c *cpuAdmission) refusedAt(gate string) {
	if c != nil {
		c.wouldRefuse.WithLabelValues(gate).Inc()
	}
}

// SetCPUAdmission applies `cpu_admission`: enforce or observe.
func (e *Engine) SetCPUAdmission(mode string) error {
	if !api.ValidCPUAdmission(mode) {
		return fmt.Errorf("sched: cpu_admission: %q is not enforce or observe", mode)
	}
	if e == nil || e.ledger == nil || e.ledger.cpuAdmission == nil {
		return nil
	}
	e.ledger.cpuAdmission.observe.Store(mode == api.CPUAdmissionObserve)
	return nil
}

// WithCPUAdmissionMetrics registers schedd_cpu_admission_would_refuse_total.
func (e *Engine) WithCPUAdmissionMetrics(reg prometheus.Registerer) *Engine {
	if e != nil && e.ledger != nil && e.ledger.cpuAdmission != nil && reg != nil {
		reg.MustRegister(e.ledger.cpuAdmission.wouldRefuse)
	}
	return e
}

// cpuObserving reports whether this engine admits on memory alone.
func (e *Engine) cpuObserving() bool {
	return e != nil && e.ledger != nil && e.ledger.cpuAdmission.observing()
}
