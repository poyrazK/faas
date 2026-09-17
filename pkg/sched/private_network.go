package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/state"
)

// PrivateNetworkRouter is the additive vmmd capability used by the
// provider-neutral private-network reconciler. It intentionally stays outside
// RoutedVMM so existing scheduler fakes and alternate routers remain source
// compatible while the product surface rolls out.
type PrivateNetworkRouter interface {
	UpdatePrivateNetwork(context.Context, string, string, []netip.Prefix) error
}

// PrivateNetworkAttachmentRouter is the live Gregale-owned dataplane seam.
// The extra identity lets vmmd attach an existing netns to its gpn-* bridge;
// provider route-only updates retain the smaller PrivateNetworkRouter shape.
type PrivateNetworkAttachmentRouter interface {
	UpdatePrivateNetworkAttachment(context.Context, string, string, string, netip.Addr, []netip.Prefix) error
}

// PrivateNetworkFabricRouter is the additive vmmd capability used to prepare
// Gregale-owned network bridges on each live compute node.
type PrivateNetworkFabricRouter interface {
	ReconcilePrivateNetworkFabric(context.Context, string, string, string, string, netip.Prefix) error
}

// PrivateNetworkFabricTransportRouter is the optional extension used once
// every active node in a region has registered an overlay address. Keeping it
// additive lets older vmmds continue the node-local bridge path during a
// rolling upgrade.
type PrivateNetworkFabricTransportRouter interface {
	ReconcilePrivateNetworkFabricWithPeers(context.Context, string, string, string, string, netip.Prefix, []netip.Addr) error
}

// PrivateNetworkRouteApplier fans one app-level CIDR set out to the vmmds that
// own its live instances. vmmd itself fans that update out to all of its local
// instances, so schedd only sends one update per node.
type PrivateNetworkRouteApplier struct {
	store  state.Store
	router PrivateNetworkRouter
	log    *slog.Logger
}

func NewPrivateNetworkRouteApplier(store state.Store, router PrivateNetworkRouter, log *slog.Logger) *PrivateNetworkRouteApplier {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkRouteApplier{store: store, router: router, log: log}
}

func (a *PrivateNetworkRouteApplier) Apply(ctx context.Context, appID string, cidrs []netip.Prefix) error {
	_, err := a.ApplyWithReport(ctx, appID, cidrs)
	return err
}

// ApplyWithReport updates each live node once and returns the per-node
// convergence result. A partial report is returned together with a joined
// error when one or more nodes fail, allowing operators to see which node is
// unhealthy while the attachment remains fail-closed.
func (a *PrivateNetworkRouteApplier) ApplyWithReport(ctx context.Context, appID string, cidrs []netip.Prefix) (privatenetwork.RouteApplyReport, error) {
	return a.applyWithReport(ctx, appID, cidrs, "", netip.Addr{})
}

func (a *PrivateNetworkRouteApplier) ApplyAttachmentWithReport(ctx context.Context, attachment state.AppPrivateNetworkAttachment) (privatenetwork.RouteApplyReport, error) {
	if api.PrivateNetworkFabricEnabled() {
		if fabric, ok := a.store.(state.PrivateNetworkStore); ok {
			if _, err := fabric.GetPrivateNetwork(ctx, attachment.AccountID, attachment.NetworkID); err == nil {
				address, allocErr := fabric.AllocatePrivateNetworkAddress(ctx, attachment.AccountID, attachment.NetworkID, "app", attachment.AppID)
				if allocErr != nil {
					return privatenetwork.RouteApplyReport{}, fmt.Errorf("private network address: %w", allocErr)
				}
				if _, ok := a.router.(PrivateNetworkAttachmentRouter); !ok {
					return privatenetwork.RouteApplyReport{}, fmt.Errorf("private network attachment update unsupported")
				}
				return a.applyWithReport(ctx, attachment.AppID, attachment.CIDRs, attachment.NetworkID, address.Address)
			} else if !errors.Is(err, state.ErrNotFound) {
				return privatenetwork.RouteApplyReport{}, fmt.Errorf("private network lookup: %w", err)
			}
		}
	}
	return a.ApplyWithReport(ctx, attachment.AppID, attachment.CIDRs)
}

func (a *PrivateNetworkRouteApplier) applyWithReport(ctx context.Context, appID string, cidrs []netip.Prefix, networkID string, address netip.Addr) (privatenetwork.RouteApplyReport, error) {
	rows, err := a.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return privatenetwork.RouteApplyReport{}, err
	}
	seen := make(map[string]struct{}, len(rows))
	for _, ins := range rows {
		if !state.IsLive(ins.State) || ins.NodeID == "" {
			continue
		}
		seen[ins.NodeID] = struct{}{}
	}
	nodes := make([]string, 0, len(seen))
	for nodeID := range seen {
		nodes = append(nodes, nodeID)
	}
	sort.Strings(nodes)
	report := privatenetwork.RouteApplyReport{Nodes: make([]privatenetwork.RouteNodeObservation, 0, len(nodes))}
	var errs []error
	for _, nodeID := range nodes {
		var applyErr error
		if networkID != "" {
			applyErr = a.router.(PrivateNetworkAttachmentRouter).UpdatePrivateNetworkAttachment(ctx, nodeID, appID, networkID, address, cidrs)
		} else {
			applyErr = a.router.UpdatePrivateNetwork(ctx, nodeID, appID, cidrs)
		}
		if applyErr != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", nodeID, applyErr))
			a.log.Warn("schedd: private network vmmd update failed", "app", appID, "node", nodeID, "err", applyErr)
			report.Nodes = append(report.Nodes, privatenetwork.RouteNodeObservation{
				NodeID: nodeID,
				Status: api.PrivateNetworkAttachmentStatusError,
				Detail: applyErr.Error(),
			})
			continue
		}
		report.Nodes = append(report.Nodes, privatenetwork.RouteNodeObservation{
			NodeID: nodeID,
			Status: api.PrivateNetworkAttachmentStatusReady,
			Detail: "routes applied",
		})
	}
	return report, errors.Join(errs...)
}

// PrivateNetworkFabricApplier fans one Gregale network definition out to every
// active compute node in the network's region. The bridge is network state, not
// workload state: provisioning it before the first wake removes the race where
// a scale-to-zero app tries to attach a side-link to a bridge that has never
// been created. External/provider attachments simply omit this applier.
type PrivateNetworkFabricApplier struct {
	store  state.Store
	router PrivateNetworkFabricRouter
	log    *slog.Logger
}

func NewPrivateNetworkFabricApplier(store state.Store, router PrivateNetworkFabricRouter, log *slog.Logger) *PrivateNetworkFabricApplier {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkFabricApplier{store: store, router: router, log: log}
}

func (a *PrivateNetworkFabricApplier) ApplyWithReport(ctx context.Context, attachment state.AppPrivateNetworkAttachment) (privatenetwork.FabricApplyReport, error) {
	rows, err := a.store.ActiveComputeNodes(ctx)
	if err != nil {
		return privatenetwork.FabricApplyReport{}, err
	}
	nodes := make([]string, 0, len(rows))
	for _, node := range rows {
		if strings.TrimSpace(node.ID) == "" || !fabricNodeInRegion(node, attachment.Region) {
			continue
		}
		nodes = append(nodes, node.ID)
	}
	sort.Strings(nodes)
	report := privatenetwork.FabricApplyReport{Nodes: make([]privatenetwork.RouteNodeObservation, 0, len(nodes))}
	var errs []error
	dynamicRouter, dynamicTransport := a.router.(PrivateNetworkFabricTransportRouter)
	for _, nodeID := range nodes {
		var reconcileErr error
		usedDynamic := false
		if dynamicTransport {
			if node, ok := findComputeNode(rows, nodeID); ok {
				if peers, ready := regionalTransportPeers(rows, node, attachment.Region); ready {
					usedDynamic = true
					reconcileErr = dynamicRouter.ReconcilePrivateNetworkFabricWithPeers(ctx, nodeID, attachment.AccountID, attachment.NetworkID, attachment.Region, firstCIDR(attachment.CIDRs), peers)
				}
			}
		}
		if !usedDynamic && reconcileErr == nil {
			// Do not apply a partial roster during a rolling upgrade; the
			// existing vmmd transport configuration remains authoritative until
			// every node has registered its overlay address.
			reconcileErr = a.router.ReconcilePrivateNetworkFabric(ctx, nodeID, attachment.AccountID, attachment.NetworkID, attachment.Region, firstCIDR(attachment.CIDRs))
		}
		if reconcileErr != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", nodeID, reconcileErr))
			a.log.Warn("schedd: private network fabric update failed", "app", attachment.AppID, "network", attachment.NetworkID, "node", nodeID, "err", reconcileErr)
			report.Nodes = append(report.Nodes, privatenetwork.RouteNodeObservation{NodeID: nodeID, Status: api.PrivateNetworkAttachmentStatusError, Detail: reconcileErr.Error()})
			continue
		}
		report.Nodes = append(report.Nodes, privatenetwork.RouteNodeObservation{NodeID: nodeID, Status: api.PrivateNetworkAttachmentStatusReady, Detail: "fabric bridge ready"})
	}
	return report, errors.Join(errs...)
}

func findComputeNode(nodes []state.ComputeNode, id string) (state.ComputeNode, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node, true
		}
	}
	return state.ComputeNode{}, false
}

// regionalTransportPeers returns the peer overlay addresses for one target
// node only when every active node in the network region has a valid address.
// During a rolling upgrade an incomplete roster deliberately falls back to
// the legacy startup-configured transport instead of applying a partial mesh.
func regionalTransportPeers(nodes []state.ComputeNode, target state.ComputeNode, region string) ([]netip.Addr, bool) {
	if target.OverlayIP == nil || !target.OverlayIP.IsValid() || !target.OverlayIP.Is4() {
		return nil, false
	}
	peers := make([]netip.Addr, 0, len(nodes))
	for _, node := range nodes {
		if !node.Active || !fabricNodeInRegion(node, region) {
			continue
		}
		if node.ID == target.ID {
			continue
		}
		if node.OverlayIP == nil || !node.OverlayIP.IsValid() || !node.OverlayIP.Is4() {
			return nil, false
		}
		peers = append(peers, *node.OverlayIP)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].String() < peers[j].String() })
	return peers, true
}

// fabricNodeInRegion keeps a region-scoped network off unrelated compute
// nodes. Older compute_nodes rows have no region; treating those rows as
// eligible preserves the single-box/default-local contract while operators
// roll out region metadata across an existing fleet.
func fabricNodeInRegion(node state.ComputeNode, region string) bool {
	want := strings.TrimSpace(region)
	if want == "" || node.Region == nil {
		return true
	}
	have := strings.TrimSpace(*node.Region)
	return have == "" || have == want
}

func firstCIDR(cidrs []netip.Prefix) netip.Prefix {
	if len(cidrs) == 0 {
		return netip.Prefix{}
	}
	return cidrs[0]
}

// PrivateNetworkAttachmentSubscriber handles the one event the poll-based
// reconciler cannot discover after the row is deleted: detach. It immediately
// clears live rules; attach/readiness transitions remain owned by the durable
// reconciler sweep.
type PrivateNetworkAttachmentSubscriber struct {
	applier *PrivateNetworkRouteApplier
	log     *slog.Logger
	pool    *pgxpool.Pool
}

func NewPrivateNetworkAttachmentSubscriber(applier *PrivateNetworkRouteApplier, log *slog.Logger) *PrivateNetworkAttachmentSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkAttachmentSubscriber{applier: applier, log: log}
}

// WithNotificationPool enables acknowledgement of a durable LISTEN fast-path
// delivery. Replay workers acknowledge rows themselves; this hook lets the
// normal notification path close the same outbox row after cleanup succeeds.
func (s *PrivateNetworkAttachmentSubscriber) WithNotificationPool(pool *pgxpool.Pool) *PrivateNetworkAttachmentSubscriber {
	s.pool = pool
	return s
}

// Handle applies one detach event. It is shared by the live LISTEN path and
// the durable replay worker so a route-cleanup failure is retryable instead of
// being logged and acknowledged as if cleanup succeeded.
func (s *PrivateNetworkAttachmentSubscriber) Handle(ctx context.Context, n db.Notification) error {
	if n.Channel != db.NotifyPrivateNetworkAttachmentChanged {
		return nil
	}
	payload, err := db.ParseAppChangedPayload(n.Payload)
	if err != nil {
		return err
	}
	if payload.Kind != "private_network_attachment" || payload.Status != "detached" {
		return nil
	}
	if err := s.applier.Apply(ctx, payload.AppID, nil); err != nil {
		return err
	}
	return nil
}

func (s *PrivateNetworkAttachmentSubscriber) Run(ctx context.Context, ch <-chan db.Notification) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			if err := s.Handle(ctx, n); err != nil {
				s.log.Warn("schedd: private network detach cleanup failed", "payload", n.Payload, "err", err)
				continue
			}
			if err := db.AcknowledgeNotification(ctx, s.pool, n); err != nil && ctx.Err() == nil {
				s.log.Warn("schedd: acknowledge private network detach", "id", n.OutboxID, "err", err)
			}
		}
	}
}
