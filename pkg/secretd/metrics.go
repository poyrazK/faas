package secretd

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// Refusal reasons (faas_secretd_refusals_total{reason}). The set is closed so
// the label stays bounded. ADR-20261110210000: with secrets opening only on
// the control plane, a refusal is what a compromised node asking for tenants
// it does not run looks like.
const (
	// ReasonUnauthenticated: the caller presented no node identity.
	ReasonUnauthenticated = "unauthenticated"
	// ReasonUnknownNode: the leaf names no active compute node.
	ReasonUnknownNode = "unknown_node"
	// ReasonNotAuthorized: the node neither owns the app nor holds a grant.
	ReasonNotAuthorized = "not_authorized"
	// ReasonBudgetExhausted: the node opened too many distinct apps this hour.
	ReasonBudgetExhausted = "budget_exhausted"
	// ReasonUnreferenced: an app-secret entry carried ciphertext while
	// secretd requires references.
	ReasonUnreferenced = "unreferenced"
	// ReasonWrongAccount: an envelope is bound to another account than the
	// app's: the caller sent another tenant's ciphertext.
	ReasonWrongAccount = "wrong_account"
	// ReasonOtherApp: a capture of another app was asked for under this app.
	ReasonOtherApp = "other_app"
	// ReasonEnvelope: an envelope or stored value did not open.
	ReasonEnvelope = "envelope"
)

// RPC names (faas_secretd_*_total{rpc}).
const (
	rpcOpenAppSecrets = "OpenAppSecrets"
	rpcGrantPeer      = "GrantPeer"
	rpcOpenCaptureKey = "OpenCaptureKey"
	rpcOpenSidecarEnv = "OpenSidecarEnv"
)

var (
	errUnreferenced = errors.New("not a reference, and this secretd opens app secrets only by reference")
	errWrongAccount = errors.New("envelope bound to another account")
)

// Metrics counts secretd's answers. A nil *Metrics counts nothing.
type Metrics struct {
	opens    *prometheus.CounterVec
	refusals *prometheus.CounterVec
}

// NewMetrics registers secretd's counters on reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		return nil, errors.New("secretd: nil prometheus registerer")
	}
	m := &Metrics{
		opens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "faas_secretd_opens_total",
			Help: "Opens secretd answered and audited, by RPC and authorization (owner, grant); GrantPeer counts grants recorded.",
		}, []string{"rpc", "via"}),
		refusals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "faas_secretd_refusals_total",
			Help: "Requests secretd refused, by RPC and reason (unauthenticated, unknown_node, not_authorized, budget_exhausted, unreferenced, wrong_account, other_app, envelope).",
		}, []string{"rpc", "reason"}),
	}
	for _, c := range []prometheus.Collector{m.opens, m.refusals} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	// Pre-create the series alerts read, so increase() sees the first
	// refusal instead of a series appearing at 1.
	for _, rpc := range []string{rpcOpenAppSecrets, rpcGrantPeer, rpcOpenCaptureKey, rpcOpenSidecarEnv} {
		for _, reason := range []string{ReasonUnauthenticated, ReasonUnknownNode, ReasonNotAuthorized,
			ReasonBudgetExhausted, ReasonUnreferenced, ReasonWrongAccount, ReasonOtherApp, ReasonEnvelope} {
			m.refusals.WithLabelValues(rpc, reason)
		}
	}
	return m, nil
}

func (m *Metrics) open(rpc, via string) {
	if m != nil {
		m.opens.WithLabelValues(rpc, via).Inc()
	}
}

func (m *Metrics) refuse(rpc, reason string) {
	if m != nil {
		m.refusals.WithLabelValues(rpc, reason).Inc()
	}
}

// envelopeReason classifies an open() error.
func envelopeReason(err error) string {
	switch {
	case errors.Is(err, errUnreferenced):
		return ReasonUnreferenced
	case errors.Is(err, errWrongAccount):
		return ReasonWrongAccount
	default:
		return ReasonEnvelope
	}
}
