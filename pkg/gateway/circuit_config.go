package gateway

// Per-app circuit breaker tuning — ADR-201 §2, H4-68.
//
// A kind=circuit_breaker edge rule tunes the instance-health breaker of its
// app; it does not create a separate breaker. Breaker state is per instance,
// not per route, so the app's highest-priority enabled rule tunes every one of
// its instances and the rule's path, method and header selectors do not
// partition it.
//
// The breaker consults its config with its lock held on the request path, so
// the lookup here never blocks: a missing or stale entry answers with the
// cached value (or the default) and refreshes in the background.

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
)

// CircuitRuleSource returns an app's compiled kind=circuit_breaker rules in
// priority order.
type CircuitRuleSource func(ctx context.Context, appID string) ([]EdgeRuleCircuitBreakerResolved, error)

// circuitConfigTTL bounds how long a customer's rule change takes to reach a
// breaker, and how often an app with traffic re-reads its rules.
const circuitConfigTTL = 30 * time.Second

// circuitConfigLoadTimeout bounds one background rule load.
const circuitConfigLoadTimeout = 5 * time.Second

type circuitConfigEntry struct {
	cfg      circuit.Config
	tuned    bool
	loadedAt time.Time
	loading  bool
}

// CircuitConfigs caches each app's breaker tuning.
type CircuitConfigs struct {
	source CircuitRuleSource
	now    func() time.Time
	log    *slog.Logger

	mu sync.Mutex
	m  map[string]*circuitConfigEntry
}

// NewCircuitConfigs builds the per-app tuning cache over source.
func NewCircuitConfigs(source CircuitRuleSource, log *slog.Logger) *CircuitConfigs {
	if log == nil {
		log = slog.Default()
	}
	return &CircuitConfigs{source: source, now: time.Now, log: log, m: make(map[string]*circuitConfigEntry)}
}

// For reports appID's tuned config; false means the group default applies.
// It never blocks on the source.
func (c *CircuitConfigs) For(appID string) (circuit.Config, bool) {
	if c == nil || c.source == nil || appID == "" {
		return circuit.Config{}, false
	}
	c.mu.Lock()
	e := c.m[appID]
	if e == nil {
		e = &circuitConfigEntry{}
		c.m[appID] = e
	}
	stale := e.loadedAt.IsZero() || c.now().Sub(e.loadedAt) >= circuitConfigTTL
	if stale && !e.loading {
		e.loading = true
		go c.load(appID)
	}
	cfg, tuned := e.cfg, e.tuned
	c.mu.Unlock()
	return cfg, tuned
}

// ForKey adapts For to a breaker key of the form appID + "\x00" + instanceID.
func (c *CircuitConfigs) ForKey(key string) (circuit.Config, bool) {
	appID, _, _ := strings.Cut(key, "\x00")
	return c.For(appID)
}

func (c *CircuitConfigs) load(appID string) {
	ctx, cancel := context.WithTimeout(context.Background(), circuitConfigLoadTimeout)
	defer cancel()
	rules, err := c.source(ctx, appID)
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[appID]
	if e == nil {
		return
	}
	e.loading = false
	if err != nil {
		// Keep the last known tuning; retry on the next lookup after the
		// TTL rather than on every request.
		e.loadedAt = c.now()
		c.log.Warn("gateway: circuit breaker rule load failed; keeping previous tuning", "app_id", appID, "err", err)
		return
	}
	e.loadedAt = c.now()
	e.cfg, e.tuned = circuitConfigFromRules(rules)
}

// circuitConfigFromRules is the first rule's tuning (rules arrive priority
// ordered).
func circuitConfigFromRules(rules []EdgeRuleCircuitBreakerResolved) (circuit.Config, bool) {
	if len(rules) == 0 {
		return circuit.Config{}, false
	}
	r := rules[0]
	return circuit.Config{
		FailureThreshold: r.FailureThreshold,
		MinRequests:      r.MinRequests,
		Window:           r.Window,
		OpenDuration:     r.OpenDuration,
		MaxOpenDuration:  r.MaxOpenDuration,
	}, true
}
