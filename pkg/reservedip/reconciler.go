// Package reservedip contains the provider-neutral route convergence loop for
// Gregale-owned reserved public IPs. The package never calls a cloud API or
// manipulates host routes directly; a Connector owns that physical boundary.
package reservedip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/networkip"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	defaultReconcileInterval = 15 * time.Second
	maxStatusDetailBytes     = 512
)

// Route is one desired public route. Generation is a fencing token: a
// connector must reject an older route operation after a release, reassignment,
// or failover has advanced the durable lease.
type Route struct {
	LeaseID    string
	Address    netip.Addr
	NodeID     string
	Generation int64
}

// Connector replaces the complete desired route set for its scope. Apply must
// be idempotent and withdraw routes absent from desired. Implementations may
// use netlink, BGP/VRRP, or an operator-managed fabric later; this contract
// keeps those choices outside the control plane.
type Connector interface {
	Apply(context.Context, []Route) error
}

// FuncConnector adapts a function for tests and early provider adapters.
type FuncConnector func(context.Context, []Route) error

func (f FuncConnector) Apply(ctx context.Context, routes []Route) error {
	return f(ctx, routes)
}

// Store is the minimum state surface required by the route reconciler. The
// generation-fenced write is what prevents a stale sweep from publishing an
// assignment after a customer release or a newer failover wins the race.
type Store interface {
	state.ReservedIPRouteStore
	ActiveComputeNodes(context.Context) ([]state.ComputeNode, error)
}

// ReconcileObservation is intentionally bounded: identifiers belong in logs,
// while metrics can consume Outcome and route counts without high-cardinality
// labels.
type ReconcileObservation struct {
	Region    string
	Outcome   string
	Duration  time.Duration
	Routes    int
	Moved     int
	Failed    int
	Contended int
}

type ReconcileSummary struct {
	Discovered int
	Routes     int
	Moved      int
	Assigned   int
	Failed     int
	Contended  int
}

type ReconcilerOptions struct {
	Region   string
	Interval time.Duration
	Now      func() time.Time
	Observe  func(ReconcileObservation)
	Logger   *slog.Logger
}

type Reconciler struct {
	store     Store
	connector Connector
	region    string
	interval  time.Duration
	now       func() time.Time
	observe   func(ReconcileObservation)
	logger    *slog.Logger
}

func NewReconciler(store Store, connector Connector, options ReconcilerOptions) (*Reconciler, error) {
	if store == nil || connector == nil {
		return nil, errors.New("reservedip: store and connector are required")
	}
	options.Region = strings.TrimSpace(options.Region)
	if options.Region == "" {
		return nil, errors.New("reservedip: region is required")
	}
	if options.Interval == 0 {
		options.Interval = defaultReconcileInterval
	}
	if options.Interval < time.Second {
		return nil, errors.New("reservedip: reconciliation interval must be at least one second")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Reconciler{
		store: store, connector: connector, region: options.Region,
		interval: options.Interval, now: options.Now, observe: options.Observe,
		logger: options.Logger,
	}, nil
}

// Sweep performs one full desired-state convergence. It first chooses a live
// node for every assigned/pending lease, then asks the connector to replace
// the complete route set, and only publishes assigned after the connector has
// succeeded. Empty desired sets are applied too, which withdraws routes for
// released leases.
func (r *Reconciler) Sweep(ctx context.Context) (ReconcileSummary, error) {
	started := r.now()
	var summary ReconcileSummary
	leases, err := r.store.ListReservedIPsForRouting(ctx, r.region)
	if err != nil {
		return summary, err
	}
	nodes, err := r.store.ActiveComputeNodes(ctx)
	if err != nil {
		return summary, err
	}
	summary.Discovered = len(leases)

	candidates := make([]networkip.Node, 0, len(nodes))
	activeByID := make(map[string]state.ComputeNode, len(nodes))
	for _, node := range nodes {
		if !node.Active || strings.TrimSpace(node.ID) == "" {
			continue
		}
		region := ""
		if node.Region != nil {
			region = strings.TrimSpace(*node.Region)
		}
		candidate := networkip.Node{ID: node.ID, Region: region, Active: true}
		candidates = append(candidates, candidate)
		activeByID[node.ID] = node
	}

	type pendingRoute struct {
		lease state.ReservedIP
		route Route
		moved bool
	}
	routes := make([]Route, 0, len(leases))
	prepared := make([]pendingRoute, 0, len(leases))
	var sweepErrs []error

	for _, lease := range leases {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if lease.AppID == "" || lease.Status == networkip.StatusAvailable {
			continue
		}
		desiredNode, ok := r.desiredNode(lease, candidates, activeByID)
		if !ok {
			if lease.Status == networkip.StatusError {
				summary.Failed++
				continue
			}
			detail := "no active compute node is available in the reserved IP region"
			if _, updateErr := r.store.UpdateReservedIPStatusIfGeneration(ctx, lease.AccountID, lease.ID, lease.Generation, networkip.StatusError, detail, lease.NodeID); updateErr != nil {
				if errors.Is(updateErr, state.ErrConflict) {
					summary.Contended++
					continue
				}
				summary.Failed++
				sweepErrs = append(sweepErrs, updateErr)
				continue
			}
			summary.Failed++
			continue
		}

		effective := lease
		moved := lease.NodeID != desiredNode
		if lease.Status == networkip.StatusError || moved {
			updated, updateErr := r.store.UpdateReservedIPStatusIfGeneration(ctx, lease.AccountID, lease.ID, lease.Generation, networkip.StatusPending, "route movement pending", desiredNode)
			if updateErr != nil {
				if errors.Is(updateErr, state.ErrConflict) {
					summary.Contended++
					continue
				}
				summary.Failed++
				sweepErrs = append(sweepErrs, updateErr)
				continue
			}
			effective = updated
			if moved {
				summary.Moved++
			}
		}
		route := Route{LeaseID: effective.ID, Address: effective.Address, NodeID: desiredNode, Generation: effective.Generation}
		routes = append(routes, route)
		prepared = append(prepared, pendingRoute{lease: effective, route: route, moved: moved})
	}

	sortRoutes(routes)
	summary.Routes = len(routes)
	if applyErr := r.connector.Apply(ctx, routes); applyErr != nil {
		detail := boundedDetail(fmt.Sprintf("reserved IP route activation failed: %v", applyErr))
		for _, item := range prepared {
			if _, updateErr := r.store.UpdateReservedIPStatusIfGeneration(ctx, item.lease.AccountID, item.lease.ID, item.lease.Generation, networkip.StatusError, detail, item.route.NodeID); updateErr != nil && !errors.Is(updateErr, state.ErrConflict) {
				sweepErrs = append(sweepErrs, updateErr)
			}
		}
		summary.Failed++
		sweepErrs = append(sweepErrs, applyErr)
		if r.observe != nil {
			r.observe(ReconcileObservation{Region: r.region, Outcome: "error", Duration: r.now().Sub(started), Routes: len(routes), Moved: summary.Moved, Failed: summary.Failed, Contended: summary.Contended})
		}
		return summary, errors.Join(sweepErrs...)
	}

	for _, item := range prepared {
		if item.lease.Status == networkip.StatusAssigned && !item.moved {
			continue
		}
		if _, updateErr := r.store.UpdateReservedIPStatusIfGeneration(ctx, item.lease.AccountID, item.lease.ID, item.lease.Generation, networkip.StatusAssigned, "route active", item.route.NodeID); updateErr != nil {
			if errors.Is(updateErr, state.ErrConflict) {
				summary.Contended++
				continue
			}
			summary.Failed++
			sweepErrs = append(sweepErrs, updateErr)
			continue
		}
		summary.Assigned++
	}

	outcome := "ready"
	if summary.Failed > 0 || len(sweepErrs) > 0 {
		outcome = "error"
	} else if summary.Contended > 0 {
		outcome = "contended"
	}
	if r.observe != nil {
		r.observe(ReconcileObservation{Region: r.region, Outcome: outcome, Duration: r.now().Sub(started), Routes: len(routes), Moved: summary.Moved, Failed: summary.Failed, Contended: summary.Contended})
	}
	return summary, errors.Join(sweepErrs...)
}

func (r *Reconciler) desiredNode(lease state.ReservedIP, candidates []networkip.Node, activeByID map[string]state.ComputeNode) (string, bool) {
	if node, ok := activeByID[lease.NodeID]; ok && nodeRegion(node, lease.Region) {
		return lease.NodeID, true
	}
	return networkip.SelectFailoverNode(lease.NodeID, lease.Region, candidates)
}

func nodeRegion(node state.ComputeNode, leaseRegion string) bool {
	if strings.TrimSpace(leaseRegion) == "" {
		return true
	}
	if node.Region == nil {
		return false
	}
	return strings.TrimSpace(*node.Region) == strings.TrimSpace(leaseRegion)
}

func sortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Address != routes[j].Address {
			return routes[i].Address.String() < routes[j].Address.String()
		}
		if routes[i].NodeID != routes[j].NodeID {
			return routes[i].NodeID < routes[j].NodeID
		}
		return routes[i].LeaseID < routes[j].LeaseID
	})
}

func (r *Reconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("reserved IP route reconciliation sweep failed", "region", r.region, "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func boundedDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) <= maxStatusDetailBytes {
		return detail
	}
	return detail[:maxStatusDetailBytes]
}
