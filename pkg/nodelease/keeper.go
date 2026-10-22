// Package nodelease keeps a compute node's liveness lease (ADR-1344 §4, P3).
//
// The node-local schedd renews its node's lease on database time. A node that
// cannot renew for longer than the lease's TTL minus a safety margin, measured
// on its own monotonic clock, is past the point where peers may take its work
// over, so it must stop at-most-once work. Phase 1 only renews the lease and
// reports that state (WouldFence); no caller acts on it yet.
//
// The margin makes the node's view the conservative one. The lease's expiry
// is set by the database when a renewal commits, and the node only starts its
// clock when it receives the reply, after that commit. So a node always
// reaches its fence point before the database considers the lease expired,
// as long as the reply took less than the margin to arrive.
package nodelease

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The lease timings live in pkg/api/limits.go (ADR-1344 §4).
const (
	DefaultTTL         = api.NodeLeaseTTL
	DefaultInterval    = api.NodeLeaseRenewInterval
	DefaultFenceMargin = api.NodeLeaseFenceMargin
)

// Store is the slice of the state store the keeper needs.
type Store interface {
	RenewNodeLease(ctx context.Context, nodeID, holder string, ttl time.Duration) (state.NodeLease, error)
}

// Metrics are the keeper's Prometheus series.
type Metrics struct {
	renewals          *prometheus.CounterVec
	secondsSinceRenew prometheus.GaugeFunc
	epoch             prometheus.Gauge
	wouldFence        prometheus.GaugeFunc
}

// Keeper renews one node's lease.
type Keeper struct {
	store    Store
	nodeID   string
	holder   string
	ttl      time.Duration
	interval time.Duration
	margin   time.Duration
	log      *slog.Logger
	now      func() time.Time

	mu      sync.Mutex
	lastOK  time.Time // monotonic: when the last successful renewal's reply arrived
	epoch   int64
	metrics *Metrics
}

// New returns a keeper for nodeID held by holder.
func New(store Store, nodeID, holder string, log *slog.Logger) (*Keeper, error) {
	if store == nil || strings.TrimSpace(nodeID) == "" || strings.TrimSpace(holder) == "" {
		return nil, errors.New("nodelease: store, node id and holder are required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Keeper{
		store: store, nodeID: nodeID, holder: holder,
		ttl: DefaultTTL, interval: DefaultInterval, margin: DefaultFenceMargin,
		log: log, now: time.Now,
	}, nil
}

// WithTiming overrides the TTL, renewal interval and fence margin. The
// interval must leave at least two renewals per fence window.
func (k *Keeper) WithTiming(ttl, interval, margin time.Duration) (*Keeper, error) {
	if margin <= 0 || ttl <= margin || interval <= 0 || 2*interval > ttl-margin {
		return nil, fmt.Errorf("nodelease: ttl %s, interval %s, margin %s leave fewer than two renewals before the fence", ttl, interval, margin)
	}
	k.ttl, k.interval, k.margin = ttl, interval, margin
	return k, nil
}

// RegisterMetrics exports the keeper's state under prefix (e.g. "schedd").
func (k *Keeper) RegisterMetrics(reg prometheus.Registerer, prefix string) {
	if reg == nil {
		return
	}
	metric := func(suffix string) string { return prefix + "_node_lease_" + suffix }
	m := &Metrics{
		renewals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metric("renewals_total"),
			Help: "Node lease renewal attempts by result: ok, held (another holder's lease is live) or error (ADR-1344 §4).",
		}, []string{"result"}),
		secondsSinceRenew: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: metric("seconds_since_renewal"),
			Help: "Seconds since this node last renewed its lease; -1 before the first renewal.",
		}, func() float64 {
			since, ok := k.sinceRenewal()
			if !ok {
				return -1
			}
			return since.Seconds()
		}),
		epoch: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: metric("epoch"),
			Help: "Epoch of this node's lease; it increases each time a new holder takes the lease over.",
		}),
		wouldFence: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: metric("would_fence"),
			Help: "1 when this node has not renewed its lease for longer than the TTL minus the fence margin. Phase 1 only reports it.",
		}, func() float64 {
			if k.WouldFence() {
				return 1
			}
			return 0
		}),
	}
	for _, result := range []string{"ok", "held", "error"} {
		m.renewals.WithLabelValues(result)
	}
	reg.MustRegister(m.renewals, m.secondsSinceRenew, m.epoch, m.wouldFence)
	k.mu.Lock()
	k.metrics = m
	k.mu.Unlock()
}

// Renew performs one renewal and records its outcome.
func (k *Keeper) Renew(ctx context.Context) error {
	lease, err := k.store.RenewNodeLease(ctx, k.nodeID, k.holder, k.ttl)
	k.mu.Lock()
	defer k.mu.Unlock()
	switch {
	case err == nil:
		// The clock starts at the reply, after the database set the expiry.
		k.lastOK = k.now()
		if lease.Epoch != k.epoch && k.epoch != 0 {
			k.log.Warn("nodelease: lease epoch changed", "node_id", k.nodeID, "previous", k.epoch, "epoch", lease.Epoch)
		}
		k.epoch = lease.Epoch
		k.count("ok")
		if k.metrics != nil {
			k.metrics.epoch.Set(float64(lease.Epoch))
		}
		return nil
	case errors.Is(err, state.ErrNodeLeaseHeld):
		k.count("held")
	default:
		k.count("error")
	}
	return err
}

func (k *Keeper) count(result string) {
	if k.metrics != nil {
		k.metrics.renewals.WithLabelValues(result).Inc()
	}
}

func (k *Keeper) sinceRenewal() (time.Duration, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.lastOK.IsZero() {
		return 0, false
	}
	return k.now().Sub(k.lastOK), true
}

// WouldFence reports whether this node is past its fence point: no renewal
// for longer than the TTL minus the margin. Before the first renewal it is
// false; a node that has never held its lease has nothing peers could take.
func (k *Keeper) WouldFence() bool {
	since, ok := k.sinceRenewal()
	return ok && since > k.ttl-k.margin
}

// Run renews the lease every interval until ctx ends.
func (k *Keeper) Run(ctx context.Context) {
	tick := time.NewTicker(k.interval)
	defer tick.Stop()
	fenced := false
	for {
		if err := k.Renew(ctx); err != nil && ctx.Err() == nil {
			k.log.Warn("nodelease: renewal failed", "node_id", k.nodeID, "err", err)
		}
		if now := k.WouldFence(); now != fenced {
			fenced = now
			if fenced {
				k.log.Error("nodelease: lease not renewed within its fence window; peers may soon take this node's work over",
					"node_id", k.nodeID, "ttl", k.ttl, "margin", k.margin)
			} else {
				k.log.Info("nodelease: lease renewed again", "node_id", k.nodeID)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Holder identifies this host boot: the hostname and, on Linux, the kernel
// boot id. A schedd restart on the same boot keeps the lease and its epoch; a
// reboot or another host takes it over with a new epoch.
func Holder() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	if boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		if id := strings.TrimSpace(string(boot)); id != "" {
			return host + "/" + id
		}
	}
	return host
}
