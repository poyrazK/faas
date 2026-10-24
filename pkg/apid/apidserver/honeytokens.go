package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/state"
)

// Honeytokens (ADR 20261024014500): decoy Gregale API keys planted where an
// attacker who read the secret store would find them. Nothing legitimate
// presents one, so a single presentation pages.

// honeytokenMetrics counts presented honeytokens. The metric name is fixed,
// not daemon-prefixed: FaasHoneytokenPresented reads it.
type honeytokenMetrics struct {
	registry  prometheus.Registerer
	presented *prometheus.CounterVec
}

func newHoneytokenMetrics(reg prometheus.Registerer) *honeytokenMetrics {
	m := &honeytokenMetrics{
		registry: reg,
		presented: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "faas_honeytoken_presented_total",
			Help: "Honeytokens presented as credentials (ADR 20261024014500). Any increase means the place a decoy was planted has been read.",
		}, []string{"kind"}),
	}
	// Servers built on one daemon registry share the counter.
	if err := reg.Register(m.presented); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			panic(err)
		}
		existing, ok := already.ExistingCollector.(*prometheus.CounterVec)
		if !ok {
			panic(err)
		}
		m.presented = existing
	}
	m.presented.WithLabelValues(state.HoneytokenKindAPIKey)
	return m
}

// wireHoneytokens hands the auth middleware the honeytoken registry when the
// store keeps one.
func (s *server) wireHoneytokens() {
	registry, ok := s.store.(state.HoneytokenStore)
	if !ok || s.authMw == nil {
		return
	}
	s.authMw.Honeytokens = honeytokenMatcher{registry: registry, hmacKey: hostHMACKeyBytes}
	s.authMw.OnHoneytoken = s.honeytokenPresented
}

// honeytokenMatcher keys a presented credential by the registry's HMAC under
// the host HMAC key, which a copy of the database does not hold.
type honeytokenMatcher struct {
	registry state.HoneytokenStore
	hmacKey  func() []byte
}

func (m honeytokenMatcher) MatchHoneytoken(ctx context.Context, token string) (state.Honeytoken, error) {
	digest, err := state.HoneytokenDigest(m.hmacKey(), token)
	if err != nil {
		// No host HMAC key loaded: no digest, so nothing can match.
		return state.Honeytoken{}, state.ErrNotFound
	}
	return m.registry.MatchHoneytoken(ctx, digest)
}

// honeytokenPresented records a presented honeytoken: a counter for the
// page, an audit event and an error log line for the investigation. The
// middleware still answers the request with the ordinary 401.
func (s *server) honeytokenPresented(r *http.Request, h state.Honeytoken) {
	if s.honeytokenMetrics != nil {
		s.honeytokenMetrics.presented.WithLabelValues(h.Kind).Inc()
	}
	data := map[string]any{
		"honeytoken_id": h.ID,
		"kind":          h.Kind,
		"label":         h.Label,
		"retired":       h.RetiredAt != nil,
		"client_ip":     clientIPFromRequest(r),
		"user_agent":    r.UserAgent(),
		"method":        r.Method,
		"path":          r.URL.Path,
	}
	if s.log != nil {
		s.log.Error("security: honeytoken presented", "honeytoken_id", h.ID, "label", h.Label, "client_ip", data["client_ip"], "path", r.URL.Path)
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "security.honeytoken_presented", nil, data)
	}
}
