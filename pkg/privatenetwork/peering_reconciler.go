package privatenetwork

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	defaultPeeringReconcileInterval = 15 * time.Second
	defaultPeeringBatchSize         = 100
	maxPeeringStatusDetailBytes     = 512
)

// PeeringRouteApplier owns the privileged dataplane boundary. Apply must
// replace the complete desired route set for one account and region, removing
// routes that are absent from routes. The control plane never shells out to
// iproute2 or calls a cloud provider directly.
type PeeringRouteApplier interface {
	Apply(context.Context, string, string, []PeeringRoute) error
}

// FuncPeeringRouteApplier adapts a function for tests and staged node
// adapters.
type FuncPeeringRouteApplier func(context.Context, string, string, []PeeringRoute) error

func (f FuncPeeringRouteApplier) Apply(ctx context.Context, accountID, region string, routes []PeeringRoute) error {
	return f(ctx, accountID, region, routes)
}

// PeeringReconcileStore is the durable state surface required by the
// provider-neutral peering worker.
type PeeringReconcileStore interface {
	state.PrivateNetworkPeeringReconcileStore
}

type PeeringReconcileObservation struct {
	AccountID string
	Region    string
	Outcome   string
	Duration  time.Duration
	Peerings  int
	Routes    int
	Failed    int
}

type PeeringReconcileSummary struct {
	Discovered int
	Peerings   int
	Routes     int
	Ready      int
	Failed     int
	Contended  int
}

type PeeringReconcilerOptions struct {
	Interval  time.Duration
	BatchSize int
	Now       func() time.Time
	Observe   func(PeeringReconcileObservation)
	Logger    *slog.Logger
}

type PeeringReconciler struct {
	store     PeeringReconcileStore
	applier   PeeringRouteApplier
	interval  time.Duration
	batchSize int
	now       func() time.Time
	observe   func(PeeringReconcileObservation)
	logger    *slog.Logger
}

func NewPeeringReconciler(store PeeringReconcileStore, applier PeeringRouteApplier, options PeeringReconcilerOptions) (*PeeringReconciler, error) {
	if store == nil || applier == nil {
		return nil, errors.New("privatenetwork: peering store and route applier are required")
	}
	if options.Interval == 0 {
		options.Interval = defaultPeeringReconcileInterval
	}
	if options.BatchSize == 0 {
		options.BatchSize = defaultPeeringBatchSize
	}
	if options.Interval < time.Second || options.BatchSize < 1 || options.BatchSize > 1000 {
		return nil, errors.New("privatenetwork: invalid peering reconciliation options")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &PeeringReconciler{
		store: store, applier: applier, interval: options.Interval, batchSize: options.BatchSize,
		now: options.Now, observe: options.Observe, logger: options.Logger,
	}, nil
}

type peeringRouteGroup struct {
	accountID string
	region    string
	rows      []state.PrivateNetworkPeering
	specs     []PeeringSpec
	blocked   map[string]struct{}
}

// Sweep applies complete desired route sets for every account/region touched
// by the bounded peering batch. A row is published ready only after the
// applier accepts the full two-way route set. Errors remain fail-closed and
// are retried by the next sweep.
func (r *PeeringReconciler) Sweep(ctx context.Context) (PeeringReconcileSummary, error) {
	rows, err := r.store.ListPrivateNetworkPeeringsForReconcile(ctx, []string{
		api.PrivateNetworkPeeringStatusPending,
		api.PrivateNetworkPeeringStatusReady,
		api.PrivateNetworkPeeringStatusError,
	}, r.batchSize)
	var summary PeeringReconcileSummary
	if err != nil {
		return summary, err
	}
	summary.Discovered = len(rows)
	groups := make(map[string]*peeringRouteGroup)
	var sweepErrs []error
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		key := row.AccountID + "\x00" + row.Region
		group := groups[key]
		if group == nil {
			group = &peeringRouteGroup{accountID: row.AccountID, region: row.Region, blocked: make(map[string]struct{})}
			groups[key] = group
		}
		group.rows = append(group.rows, row)
		left, leftErr := r.store.GetPrivateNetwork(ctx, row.AccountID, row.LeftNetworkID)
		right, rightErr := r.store.GetPrivateNetwork(ctx, row.AccountID, row.RightNetworkID)
		if leftErr != nil || rightErr != nil {
			detail := "peering network definition is unavailable; traffic remains blocked"
			if leftErr != nil && !errors.Is(leftErr, state.ErrNotFound) {
				detail = boundedPeeringDetail(fmt.Sprintf("left network lookup failed: %v", leftErr))
			} else if rightErr != nil && !errors.Is(rightErr, state.ErrNotFound) {
				detail = boundedPeeringDetail(fmt.Sprintf("right network lookup failed: %v", rightErr))
			}
			if _, updateErr := r.store.UpdatePrivateNetworkPeeringStatus(ctx, row.AccountID, row.ID, api.PrivateNetworkPeeringStatusError, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
				sweepErrs = append(sweepErrs, updateErr)
			}
			group.blocked[row.ID] = struct{}{}
			summary.Failed++
			continue
		}
		if left.Status != api.PrivateNetworkStatusReady || right.Status != api.PrivateNetworkStatusReady {
			detail := "one or more private networks are not ready; traffic remains blocked"
			if _, updateErr := r.store.UpdatePrivateNetworkPeeringStatus(ctx, row.AccountID, row.ID, api.PrivateNetworkPeeringStatusError, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
				sweepErrs = append(sweepErrs, updateErr)
			}
			group.blocked[row.ID] = struct{}{}
			summary.Failed++
			continue
		}
		spec := PeeringSpec{
			ID: row.ID, AccountID: row.AccountID, Region: row.Region,
			Left:  FabricSpec{AccountID: left.AccountID, NetworkID: left.ID, Region: left.Region, CIDR: left.CIDR},
			Right: FabricSpec{AccountID: right.AccountID, NetworkID: right.ID, Region: right.Region, CIDR: right.CIDR},
		}
		group.specs = append(group.specs, spec)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		started := r.now()
		routes, planErr := BuildPeeringRoutes(group.specs)
		summary.Peerings += len(group.rows)
		summary.Routes += len(routes)
		if planErr != nil {
			detail := boundedPeeringDetail(fmt.Sprintf("private network peering plan rejected: %v", planErr))
			for _, row := range group.rows {
				if _, blocked := group.blocked[row.ID]; blocked {
					continue
				}
				if _, updateErr := r.store.UpdatePrivateNetworkPeeringStatus(ctx, row.AccountID, row.ID, api.PrivateNetworkPeeringStatusError, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
					sweepErrs = append(sweepErrs, updateErr)
				}
			}
			summary.Failed += len(group.specs)
			r.emitObservation(group, "error", started, len(routes), len(group.rows))
			continue
		}
		applyErr := r.applier.Apply(ctx, group.accountID, group.region, routes)
		if applyErr != nil {
			detail := boundedPeeringDetail(fmt.Sprintf("private network peering route activation failed: %v", applyErr))
			for _, row := range group.rows {
				if _, blocked := group.blocked[row.ID]; blocked {
					continue
				}
				if _, updateErr := r.store.UpdatePrivateNetworkPeeringStatus(ctx, row.AccountID, row.ID, api.PrivateNetworkPeeringStatusError, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
					sweepErrs = append(sweepErrs, updateErr)
				}
			}
			summary.Failed += len(group.specs)
			sweepErrs = append(sweepErrs, applyErr)
			r.emitObservation(group, "error", started, len(routes), len(group.rows))
			continue
		}
		for _, row := range group.rows {
			if _, blocked := group.blocked[row.ID]; blocked {
				continue
			}
			if row.Status == api.PrivateNetworkPeeringStatusReady {
				continue
			}
			if _, updateErr := r.store.UpdatePrivateNetworkPeeringStatus(ctx, row.AccountID, row.ID, api.PrivateNetworkPeeringStatusReady, "private network routes active"); updateErr != nil {
				if errors.Is(updateErr, state.ErrNotFound) {
					summary.Contended++
					continue
				}
				summary.Failed++
				sweepErrs = append(sweepErrs, updateErr)
				continue
			}
			summary.Ready++
		}
		outcome, failed := "ready", 0
		if len(group.blocked) > 0 {
			outcome, failed = "blocked", len(group.blocked)
		}
		r.emitObservation(group, outcome, started, len(routes), failed)
	}
	return summary, errors.Join(sweepErrs...)
}

func (r *PeeringReconciler) emitObservation(group *peeringRouteGroup, outcome string, started time.Time, routes, failed int) {
	if r.observe == nil {
		return
	}
	r.observe(PeeringReconcileObservation{
		AccountID: group.accountID, Region: group.region, Outcome: outcome,
		Duration: r.now().Sub(started), Peerings: len(group.rows), Routes: routes, Failed: failed,
	})
}

func (r *PeeringReconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("private network peering reconciliation sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func boundedPeeringDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) <= maxPeeringStatusDetailBytes {
		return detail
	}
	return detail[:maxPeeringStatusDetailBytes]
}
