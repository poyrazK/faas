package main

import "github.com/prometheus/client_golang/prometheus"

// Workload-identity token request outcomes (ADR-1141). not_configured counts
// apps asking a node that has no signing key: their queue leases, Flags and
// cloud federation calls fail until the key is installed.
const (
	workloadIdentityMinted           = "minted"
	workloadIdentityNotConfigured    = "not_configured"
	workloadIdentityInvalidAudience  = "invalid_audience"
	workloadIdentityInstanceNotFound = "instance_not_found"
	workloadIdentityInvalidRequest   = "invalid_request"
)

type workloadIdentityMetrics struct {
	tokens *prometheus.CounterVec
}

func newWorkloadIdentityMetrics(reg prometheus.Registerer) *workloadIdentityMetrics {
	m := &workloadIdentityMetrics{tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vmmd_workload_identity_tokens_total",
		Help: "Guest workload-identity token requests by result (ADR-1141).",
	}, []string{"result"})}
	for _, result := range []string{workloadIdentityMinted, workloadIdentityNotConfigured, workloadIdentityInvalidAudience,
		workloadIdentityInstanceNotFound, workloadIdentityInvalidRequest} {
		m.tokens.WithLabelValues(result)
	}
	if reg != nil {
		reg.MustRegister(m.tokens)
	}
	return m
}

func (m *workloadIdentityMetrics) observe(result string) {
	if m != nil {
		m.tokens.WithLabelValues(result).Inc()
	}
}
