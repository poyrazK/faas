// Package privatenetwork contains the provider-neutral runtime contract for
// app VPC attachments. Providers only answer whether an attachment is usable;
// the reconciler owns durable state transitions and route activation.
package privatenetwork

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	defaultReconcileInterval = 15 * time.Second
	defaultBatchSize         = 50
	maxStatusDetailBytes     = 512
)

// Connector checks the provider-side attachment without changing Gregale's
// durable state. Implementations may call a cloud API, a local network agent,
// or an operator-managed control plane. A not-ready result is not an error:
// it keeps the attachment pending and therefore fail-closed.
type Connector interface {
	Check(ctx context.Context, attachment state.AppPrivateNetworkAttachment) (CheckResult, error)
}

type CheckResult struct {
	Ready  bool
	Detail string
}

// RouteApplier activates the exact CIDRs requested by the customer. It must be
// idempotent and replace the app's previous set atomically. The reconciler
// calls it before publishing status=ready, so a failed route update can never
// expose a falsely-ready attachment.
type RouteApplier interface {
	Apply(ctx context.Context, appID string, cidrs []netip.Prefix) error
}

// RouteNodeObservation describes the result for one compute node. A route
// applier may return a partial report when one node fails; the reconciler uses
// it for operator diagnostics while the attachment remains fail-closed.
type RouteNodeObservation struct {
	NodeID string
	Status string
	Detail string
}

// RouteApplyReport is the optional node-level result returned by a route
// applier. Keeping this additive preserves the small RouteApplier interface
// for existing providers and test doubles.
type RouteApplyReport struct {
	Nodes []RouteNodeObservation
}

// RouteReportingApplier is an optional extension implemented by appliers that
// can report per-node convergence. The reconciler still accepts a plain
// RouteApplier and records an empty node report for those implementations.
type RouteReportingApplier interface {
	RouteApplier
	ApplyWithReport(ctx context.Context, appID string, cidrs []netip.Prefix) (RouteApplyReport, error)
}

// AttachmentRouteReportingApplier is the optional Gregale-owned extension.
// It carries the network identity and stable member address so an already-live
// workload can receive the same private side-link as a fresh wake. External
// provider attachments continue through RouteReportingApplier unchanged.
type AttachmentRouteReportingApplier interface {
	RouteReportingApplier
	ApplyAttachmentWithReport(ctx context.Context, attachment state.AppPrivateNetworkAttachment) (RouteApplyReport, error)
}

// FabricApplier prepares the node-local realization of a Gregale-owned
// network before route policy is published. It is optional so external
// provider attachments and older test doubles can keep using RouteApplier
// alone during the staged rollout.
type FabricApplier interface {
	ApplyWithReport(ctx context.Context, attachment state.AppPrivateNetworkAttachment) (FabricApplyReport, error)
}

type FabricApplyReport struct {
	Nodes []RouteNodeObservation
}

type NotifyFunc func(ctx context.Context, attachment state.AppPrivateNetworkAttachment)

type ReconcileObservation struct {
	AppID       string
	Status      string
	Outcome     string
	Duration    time.Duration
	Nodes       []RouteNodeObservation
	FabricNodes []RouteNodeObservation
}

type ReconcileSummary struct {
	Discovered int
	Ready      int
	Pending    int
	Failed     int
	Contended  int
	NodeReady  int
	NodeFailed int
}

type ReconcilerOptions struct {
	Interval  time.Duration
	BatchSize int
	Now       func() time.Time
	Observe   func(ReconcileObservation)
	Notify    NotifyFunc
	Logger    *slog.Logger
	Fabric    FabricApplier
}

// Reconciler is intentionally independent of apid and vmmd. This keeps the
// provider adapter replaceable while the same lifecycle works in local,
// DigitalOcean, and future VPC implementations.
type Reconciler struct {
	store     state.AppPrivateNetworkAttachmentReconcileStore
	connector Connector
	applier   RouteApplier
	fabric    FabricApplier
	interval  time.Duration
	batchSize int
	now       func() time.Time
	observe   func(ReconcileObservation)
	notify    NotifyFunc
	logger    *slog.Logger
}

func NewReconciler(store state.AppPrivateNetworkAttachmentReconcileStore, connector Connector, applier RouteApplier, options ReconcilerOptions) (*Reconciler, error) {
	if store == nil || connector == nil || applier == nil {
		return nil, errors.New("privatenetwork: store, connector, and route applier are required")
	}
	if options.Interval == 0 {
		options.Interval = defaultReconcileInterval
	}
	if options.BatchSize == 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.Interval < time.Second || options.BatchSize < 1 || options.BatchSize > 1000 {
		return nil, errors.New("privatenetwork: invalid reconciliation options")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Reconciler{
		store: store, connector: connector, applier: applier, fabric: options.Fabric,
		interval: options.Interval, batchSize: options.BatchSize,
		now: options.Now, observe: options.Observe, notify: options.Notify,
		logger: options.Logger,
	}, nil
}

func (r *Reconciler) Sweep(ctx context.Context) (ReconcileSummary, error) {
	var summary ReconcileSummary
	rows, err := r.store.ListAppPrivateNetworkAttachments(ctx,
		[]string{api.PrivateNetworkAttachmentStatusPending, api.PrivateNetworkAttachmentStatusReady, api.PrivateNetworkAttachmentStatusError}, r.batchSize)
	if err != nil {
		return summary, err
	}
	summary.Discovered = len(rows)
	var sweepErrs []error
	for _, attachment := range rows {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		started := r.now()
		outcome := "pending"
		var routeReport RouteApplyReport
		var fabricReport FabricApplyReport
		check, checkErr := r.connector.Check(ctx, attachment)
		switch {
		case checkErr != nil:
			outcome = "error"
			summary.Failed++
			detail := boundedDetail(checkErr.Error())
			if _, updateErr := r.store.UpdateAppPrivateNetworkAttachmentStatus(ctx, attachment.AccountID, attachment.AppID, api.PrivateNetworkAttachmentStatusError, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
				sweepErrs = append(sweepErrs, updateErr)
			} else {
				sweepErrs = append(sweepErrs, checkErr)
			}
		case !check.Ready:
			summary.Pending++
			detail := boundedDetail(check.Detail)
			if detail == "" {
				detail = "provider attachment is not ready; traffic remains blocked"
			}
			if _, updateErr := r.store.UpdateAppPrivateNetworkAttachmentStatus(ctx, attachment.AccountID, attachment.AppID, api.PrivateNetworkAttachmentStatusPending, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
				sweepErrs = append(sweepErrs, updateErr)
			}
		default:
			if err := validateAttachmentCIDRs(attachment.CIDRs); err != nil {
				summary.Failed++
				detail := boundedDetail(err.Error())
				if _, updateErr := r.store.UpdateAppPrivateNetworkAttachmentStatus(ctx, attachment.AccountID, attachment.AppID, api.PrivateNetworkAttachmentStatusError, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
					sweepErrs = append(sweepErrs, updateErr)
				} else {
					sweepErrs = append(sweepErrs, err)
				}
				break
			}
			if r.fabric != nil {
				fabricReport, checkErr = r.fabric.ApplyWithReport(ctx, attachment)
				if checkErr != nil {
					summary.Failed++
					detail := boundedDetail(fmt.Sprintf("network fabric activation failed: %v", checkErr))
					if _, updateErr := r.store.UpdateAppPrivateNetworkAttachmentStatus(ctx, attachment.AccountID, attachment.AppID, api.PrivateNetworkAttachmentStatusError, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
						sweepErrs = append(sweepErrs, updateErr)
					} else {
						sweepErrs = append(sweepErrs, checkErr)
					}
					outcome = "error"
					break
				}
			}
			if reporting, ok := r.applier.(AttachmentRouteReportingApplier); ok {
				routeReport, checkErr = reporting.ApplyAttachmentWithReport(ctx, attachment)
			} else if reporting, ok := r.applier.(RouteReportingApplier); ok {
				routeReport, checkErr = reporting.ApplyWithReport(ctx, attachment.AppID, attachment.CIDRs)
			} else {
				checkErr = r.applier.Apply(ctx, attachment.AppID, attachment.CIDRs)
			}
			for _, node := range routeReport.Nodes {
				switch node.Status {
				case api.PrivateNetworkAttachmentStatusReady:
					summary.NodeReady++
				case api.PrivateNetworkAttachmentStatusError:
					summary.NodeFailed++
				}
			}
			if checkErr != nil {
				summary.Failed++
				detail := boundedDetail(fmt.Sprintf("route activation failed: %v", checkErr))
				if _, updateErr := r.store.UpdateAppPrivateNetworkAttachmentStatus(ctx, attachment.AccountID, attachment.AppID, api.PrivateNetworkAttachmentStatusError, detail); updateErr != nil && !errors.Is(updateErr, state.ErrNotFound) {
					sweepErrs = append(sweepErrs, updateErr)
				} else {
					sweepErrs = append(sweepErrs, checkErr)
				}
				outcome = "error"
				break
			}
			updated, updateErr := r.store.UpdateAppPrivateNetworkAttachmentStatus(ctx, attachment.AccountID, attachment.AppID, api.PrivateNetworkAttachmentStatusReady, readyDetail(check.Detail))
			if updateErr != nil {
				if errors.Is(updateErr, state.ErrNotFound) {
					summary.Contended++
					outcome = "contended"
					break
				}
				summary.Failed++
				sweepErrs = append(sweepErrs, updateErr)
				outcome = "error"
				break
			}
			summary.Ready++
			outcome = "ready"
			if r.notify != nil {
				r.notify(ctx, updated)
			}
		}
		if r.observe != nil {
			r.observe(ReconcileObservation{
				AppID: attachment.AppID, Status: attachment.Status, Outcome: outcome,
				Duration: r.now().Sub(started), Nodes: cloneRouteNodeObservations(routeReport.Nodes),
				FabricNodes: cloneRouteNodeObservations(fabricReport.Nodes),
			})
		}
	}
	return summary, errors.Join(sweepErrs...)
}

func cloneRouteNodeObservations(in []RouteNodeObservation) []RouteNodeObservation {
	if len(in) == 0 {
		return nil
	}
	out := make([]RouteNodeObservation, len(in))
	copy(out, in)
	return out
}

func (r *Reconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("private network reconciliation sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func validateAttachmentCIDRs(cidrs []netip.Prefix) error {
	raw := make([]string, 0, len(cidrs))
	for _, prefix := range cidrs {
		raw = append(raw, prefix.String())
	}
	_, err := api.ValidatePrivateNetworkCIDRs(raw, api.PrivateNetworkAttachmentMaxCIDRs)
	return err
}

func readyDetail(providerDetail string) string {
	if detail := boundedDetail(providerDetail); detail != "" {
		return "private network route active; " + detail
	}
	return "private network route active"
}

func boundedDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) <= maxStatusDetailBytes {
		return detail
	}
	return detail[:maxStatusDetailBytes]
}
