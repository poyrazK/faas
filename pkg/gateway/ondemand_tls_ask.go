// pkg/gateway/ondemand_tls_ask.go — the permission check behind ADR-520's
// self-hosted custom-domain TLS.
//
// The public edge (Caddy) terminates TLS. For any hostname without a
// statically configured site it uses on-demand TLS: before it loads a
// certificate from storage, obtains a new one, or renews one, it calls
//
//	GET <ask URL>?domain=<hostname>
//
// and proceeds only on 200. gatewayd-public serves that URL on its loopback
// control listener. The answer must match what gatewayd-internal routes:
// a verified exact custom domain, or a host below a verified wildcard custom
// domain with no exact row of its own. Platform hostnames are never issued
// on demand; the edge carries static certificates for them.
//
// Wildcard hosts are an unbounded set chosen by whoever sends a TLS
// ClientHello, so each wildcard gets a durable budget of new hosts per week
// (state.CustomDomainTLSHostStore). The budget is durable because the edge
// asks again on every reload and renewal: an in-memory limit would refuse
// legitimate certificates after each gatewayd-public restart.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// OnDemandTLSAskPath is the control-listener path the edge's on-demand TLS
// "ask" URL points at.
const OnDemandTLSAskPath = "/v1/internal/tls/ask"

// onDemandTLSWildcardWindow is the rolling window of
// api.OnDemandTLSWildcardNewHostsPerWeek.
const onDemandTLSWildcardWindow = 7 * 24 * time.Hour

// onDemandTLSLookupTimeout bounds each store call. The edge holds the
// client's TLS handshake while it waits for the answer.
const onDemandTLSLookupTimeout = 2 * time.Second

// On-demand TLS decisions. Closed set: they are the `decision` label of
// gateway_tls_on_demand_ask_total.
const (
	OnDemandTLSAllowExact     = "allow_exact"
	OnDemandTLSAllowWildcard  = "allow_wildcard"
	OnDemandTLSDenyUnverified = "deny_unverified"
	OnDemandTLSDenyBudget     = "deny_wildcard_budget"
	OnDemandTLSDenyUnknown    = "deny_unknown"
	OnDemandTLSDenyPlatform   = "deny_platform"
	OnDemandTLSDenyInvalid    = "deny_invalid"
	OnDemandTLSDenyOverload   = "deny_overload"
	OnDemandTLSDenyError      = "deny_error"
)

// OnDemandTLSStore is the state the policy reads, plus the wildcard
// admission ledger it writes.
type OnDemandTLSStore interface {
	DomainByName(ctx context.Context, domain string) (state.CustomDomain, error)
	state.CustomDomainWildcardStore
	state.CustomDomainTLSHostStore
}

// OnDemandTLSPolicy decides whether the edge may hold a certificate for a
// hostname.
type OnDemandTLSPolicy struct {
	store      OnDemandTLSStore
	appsDomain string
	now        func() time.Time
	log        *slog.Logger
	decisions  *prometheus.CounterVec

	mu       sync.Mutex
	tokens   float64
	refilled time.Time
	unknown  map[string]time.Time // host → negative-cache expiry
}

// NewOnDemandTLSPolicy builds the policy. appsDomain is the platform zone
// (FAAS_APPS_DOMAIN); it and every name below it are refused. reg may be nil.
func NewOnDemandTLSPolicy(store OnDemandTLSStore, appsDomain string, reg prometheus.Registerer, log *slog.Logger) *OnDemandTLSPolicy {
	if log == nil {
		log = slog.Default()
	}
	p := &OnDemandTLSPolicy{
		store:      store,
		appsDomain: normalizeTLSHost(appsDomain),
		now:        time.Now,
		log:        log,
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_tls_on_demand_ask_total",
			Help: "On-demand TLS permission checks from the public edge, by decision (ADR-520).",
		}, []string{"decision"}),
	}
	if reg != nil {
		reg.MustRegister(p.decisions)
	}
	return p
}

// SetNow overrides the clock for tests.
func (p *OnDemandTLSPolicy) SetNow(now func() time.Time) { p.now = now }

// Decide returns whether a certificate is allowed for host and the decision
// label explaining why.
func (p *OnDemandTLSPolicy) Decide(ctx context.Context, rawHost string) (bool, string) {
	host := normalizeTLSHost(rawHost)
	if host == "" || strings.Contains(host, "*") || state.ValidateCustomDomainName(host) != nil {
		return false, OnDemandTLSDenyInvalid
	}
	if p.appsDomain != "" && (host == p.appsDomain || strings.HasSuffix(host, "."+p.appsDomain)) {
		return false, OnDemandTLSDenyPlatform
	}
	if p.store == nil {
		return false, OnDemandTLSDenyError
	}
	if decision, ok := p.admitLookup(host); !ok {
		return false, decision
	}
	// An exact row wins over any wildcard, verified or not: an unverified
	// exact reservation shadows the broader wildcard in routing too.
	lookupCtx, cancel := context.WithTimeout(ctx, onDemandTLSLookupTimeout)
	exact, err := p.store.DomainByName(lookupCtx, host)
	cancel()
	switch {
	case err == nil && exact.Verified():
		return true, OnDemandTLSAllowExact
	case err == nil:
		return false, OnDemandTLSDenyUnverified
	case !errors.Is(err, state.ErrNotFound):
		p.log.Warn("gateway: on-demand tls exact lookup failed; denying", "host", logsanitize.Field(host), "err", err)
		return false, OnDemandTLSDenyError
	}
	return p.decideWildcard(ctx, host)
}

// decideWildcard admits a host with no exact row when a verified wildcard
// covers it and the wildcard's durable issuance budget allows it.
func (p *OnDemandTLSPolicy) decideWildcard(ctx context.Context, host string) (bool, string) {
	lookupCtx, cancel := context.WithTimeout(ctx, onDemandTLSLookupTimeout)
	defer cancel()
	wildcard, err := p.store.WildcardDomainForHost(lookupCtx, host)
	switch {
	case errors.Is(err, state.ErrNotFound):
		p.rememberUnknown(host)
		return false, OnDemandTLSDenyUnknown
	case err != nil:
		p.log.Warn("gateway: on-demand tls wildcard lookup failed; denying", "host", logsanitize.Field(host), "err", err)
		return false, OnDemandTLSDenyError
	case !wildcard.Verified():
		return false, OnDemandTLSDenyUnverified
	}
	admitted, err := p.store.AdmitCustomDomainTLSHost(lookupCtx, wildcard.Domain, host, p.now(),
		onDemandTLSWildcardWindow, api.OnDemandTLSWildcardNewHostsPerWeek)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return false, OnDemandTLSDenyUnknown
	case err != nil:
		p.log.Warn("gateway: on-demand tls wildcard admission failed; denying", "host", logsanitize.Field(host), "err", err)
		return false, OnDemandTLSDenyError
	case !admitted:
		p.log.Debug("gateway: on-demand tls wildcard budget exhausted", "host", logsanitize.Field(host),
			"wildcard", wildcard.Domain, "limit", api.OnDemandTLSWildcardNewHostsPerWeek)
		return false, OnDemandTLSDenyBudget
	}
	return true, OnDemandTLSAllowWildcard
}

// Handler serves the edge's ask URL. Only loopback peers are answered: the
// edge runs on the same host, and the control listener may be bound wider
// for health probes.
func (p *OnDemandTLSPolicy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !loopbackPeer(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		allowed, decision := p.Decide(r.Context(), r.URL.Query().Get("domain"))
		p.decisions.WithLabelValues(decision).Inc()
		w.Header().Set("Cache-Control", "no-store")
		if !allowed {
			http.Error(w, decision, http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// admitLookup decides whether host may cost store lookups: a recently
// unknown host is denied from the negative cache, otherwise a token bucket
// bounds lookups per second.
func (p *OnDemandTLSPolicy) admitLookup(host string) (string, bool) {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if until, ok := p.unknown[host]; ok {
		if now.Before(until) {
			return OnDemandTLSDenyUnknown, false
		}
		delete(p.unknown, host)
	}
	burst := float64(api.OnDemandTLSAskLookupBurst)
	if p.refilled.IsZero() {
		p.tokens, p.refilled = burst, now
	}
	if elapsed := now.Sub(p.refilled).Seconds(); elapsed > 0 {
		p.tokens = min(burst, p.tokens+elapsed*api.OnDemandTLSAskLookupsPerSecond)
		p.refilled = now
	}
	if p.tokens < 1 {
		return OnDemandTLSDenyOverload, false
	}
	p.tokens--
	return "", true
}

// rememberUnknown negative-caches a host with no custom-domain row. The
// cache is dropped wholesale when full; it only saves lookups.
func (p *OnDemandTLSPolicy) rememberUnknown(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unknown == nil || len(p.unknown) >= api.OnDemandTLSAskNegativeCacheEntries {
		p.unknown = make(map[string]time.Time)
	}
	p.unknown[host] = p.now().Add(time.Duration(api.OnDemandTLSAskNegativeCacheSeconds) * time.Second)
}

func normalizeTLSHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func loopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}
