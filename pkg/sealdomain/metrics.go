package sealdomain

import "github.com/prometheus/client_golang/prometheus"

// RegisterLoaded exports faas_seal_domain_loaded{domain}: 1 when the daemon
// loaded the domain's identity at start. Writers may seal to the domain
// alone only once every reader reports 1; FaasSealDomainIdentityMissing
// fires for a reader that reports 0 while another reader of the domain
// reports 1.
func RegisterLoaded(reg prometheus.Registerer, d Domain, loaded bool) {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "faas_seal_domain_loaded",
		Help: "1 when this daemon loaded the seal domain's identity at start (ADR 20261022111812).",
	}, []string{"domain"})
	value := 0.0
	if loaded {
		value = 1
	}
	g.WithLabelValues(string(d)).Set(value)
	reg.MustRegister(g)
}

// RegisterBindingRequired exports faas_runtime_secret_binding_required: 1
// when this vmmd refuses runtime secrets that carry no account binding
// (ADR-20261023173624, contract step). The switch is read at start, so the
// gauge says which vmmds enforce it after a converge.
func RegisterBindingRequired(reg prometheus.Registerer, required bool) {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "faas_runtime_secret_binding_required",
		Help: "1 when this vmmd refuses runtime secrets with no account binding (ADR-20261023173624).",
	})
	if required {
		g.Set(1)
	}
	reg.MustRegister(g)
}
