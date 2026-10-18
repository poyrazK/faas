package nodelease

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// LeaseReader is the slice of the state store the takeover guard needs.
type LeaseReader interface {
	GetNodeLease(ctx context.Context, nodeID string) (state.NodeLease, error)
}

// Guard decides whether a peer may treat a node as gone (ADR-1344 §4,
// phase 2). The heartbeat rules still have to agree first; the guard only
// adds a veto: a node whose lease is still live, by database time plus a
// margin, keeps its work no matter how old its heartbeat looks.
type Guard struct {
	store    LeaseReader
	margin   time.Duration
	log      *slog.Logger
	refusals *prometheus.CounterVec
}

// NewGuard returns a guard reading leases from store.
func NewGuard(store LeaseReader, log *slog.Logger) (*Guard, error) {
	if store == nil {
		return nil, errors.New("nodelease: guard needs a lease store")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Guard{store: store, margin: api.NodeLeaseTakeoverMargin, log: log}, nil
}

// RegisterMetrics exports how often the guard vetoed a takeover.
func (g *Guard) RegisterMetrics(reg prometheus.Registerer, prefix string) {
	if g == nil || reg == nil {
		return
	}
	g.refusals = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_node_lease_takeover_refusals_total",
		Help: "Takeovers of a stale-looking node the lease guard refused, by path and reason (ADR-1344 §4 phase 2).",
	}, []string{"path", "reason"})
	reg.MustRegister(g.refusals)
}

// MayTakeOver reports whether the caller on path may act on nodeID being
// gone:
//   - no lease row (the node never renewed one): yes, the heartbeat rules
//     decide as before, so a node without the lease can still be recovered;
//   - the lease expired at least the margin ago by database time: yes;
//   - the lease is still live: no;
//   - the lease cannot be read: no, because acting on a node that may still
//     be running is what the lease exists to prevent.
//
// A nil guard always allows, which is the guard being off.
func (g *Guard) MayTakeOver(ctx context.Context, path, nodeID string) bool {
	if g == nil {
		return true
	}
	lease, err := g.store.GetNodeLease(ctx, nodeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return true
	case err != nil:
		g.refuse(path, "unreadable", nodeID, err)
		return false
	case lease.ExpiredFor(g.margin):
		return true
	default:
		g.refuse(path, "lease_live", nodeID, nil)
		return false
	}
}

func (g *Guard) refuse(path, reason, nodeID string, err error) {
	if g.refusals != nil {
		g.refusals.WithLabelValues(path, reason).Inc()
	}
	g.log.Info("nodelease: takeover of a stale-looking node refused", "path", path, "node_id", nodeID, "reason", reason, "err", err)
}
