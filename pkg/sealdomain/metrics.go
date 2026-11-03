package sealdomain

import "github.com/prometheus/client_golang/prometheus"

// RegisterLoaded exports faas_seal_domain_loaded{domain}: 1 when the daemon
// loaded the domain's identity at start. Writers may seal to the domain
// alone only once every reader reports 1; FaasSealDomainIdentityMissing
// fires for a reader that reports 0 while another reader of the domain
// reports 1.
func RegisterLoaded(reg prometheus.Registerer, d Domain, loaded bool) {
	RegisterLoadedDomains(reg, map[Domain]bool{d: loaded})
}

// RegisterLoadedDomains is RegisterLoaded for a daemon that reads several
// domains, as vmmd reads runtime and build (ADR-20261026091500). The gauge
// is registered once, with one series per domain.
func RegisterLoadedDomains(reg prometheus.Registerer, loaded map[Domain]bool) {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "faas_seal_domain_loaded",
		Help: "1 when this daemon loaded the seal domain's identity at start (ADR 20261022111812).",
	}, []string{"domain"})
	for d, ok := range loaded {
		value := 0.0
		if ok {
			value = 1
		}
		g.WithLabelValues(string(d)).Set(value)
	}
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

// RegisterSignatureEnforcement exports
// faas_runtime_secret_signature_enforcement (ADR 20261028161502, amendment
// 1): 0 when this vmmd does not check runtime secret signatures, 1 when it
// refuses a signature that does not verify, 2 when it also refuses unsigned
// runtime secrets. Read at start, like the binding switch.
func RegisterSignatureEnforcement(reg prometheus.Registerer, verifying, required bool) {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "faas_runtime_secret_signature_enforcement",
		Help: "0 off, 1 refuses runtime secrets whose signature does not verify, 2 also refuses unsigned ones (ADR 20261028161502).",
	})
	switch {
	case required:
		g.Set(2)
	case verifying:
		g.Set(1)
	}
	reg.MustRegister(g)
}

// RegisterLegacyIdentities exports faas_seal_domain_legacy_identities: how
// many fleet and host identities vmmd opens runtime secrets and registry
// passwords with besides the runtime and build domains (ADR
// 20261022111812). A contracted vmmd reports 0. Read at start, like the
// binding switch.
func RegisterLegacyIdentities(reg prometheus.Registerer, n int) {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "faas_seal_domain_legacy_identities",
		Help: "Fleet and host identities this vmmd opens runtime secrets and registry passwords with (ADR 20261022111812); 0 once contracted.",
	})
	g.Set(float64(n))
	reg.MustRegister(g)
}
