package sched

import (
	"context"
	"encoding/json"
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

type PrivateNetworkPolicyRouter interface {
	UpdatePrivateNetworkWithPolicy(context.Context, string, string, []netip.Prefix, []netip.Prefix) error
}

type PrivateNetworkFirewallPolicyRouter interface {
	UpdatePrivateNetworkWithFirewall(context.Context, string, string, []netip.Prefix, []netip.Prefix, []api.PrivateNetworkFirewallRule) error
}

// PrivateNetworkAttachmentRouter is the live Gregale-owned dataplane seam.
// The extra identity lets vmmd attach an existing netns to its gpn-* bridge;
// provider route-only updates retain the smaller PrivateNetworkRouter shape.
type PrivateNetworkAttachmentRouter interface {
	UpdatePrivateNetworkAttachment(context.Context, string, string, string, netip.Addr, []netip.Prefix) error
}

type PrivateNetworkPolicyAttachmentRouter interface {
	UpdatePrivateNetworkAttachmentWithPolicy(context.Context, string, string, string, netip.Addr, []netip.Prefix, []netip.Prefix) error
}

type PrivateNetworkFirewallPolicyAttachmentRouter interface {
	UpdatePrivateNetworkAttachmentWithFirewall(context.Context, string, string, string, netip.Addr, []netip.Prefix, []netip.Prefix, []api.PrivateNetworkFirewallRule) error
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

// PrivateNetworkFabricTeardownRouter is the additive vmmd capability used to
// remove a node-local bridge after a Gregale-owned network is deleted.
type PrivateNetworkFabricTeardownRouter interface {
	RemovePrivateNetworkFabric(context.Context, string, string, string, string, netip.Prefix) error
}

// PrivateNetworkRouteApplier fans one app-level CIDR set out to the vmmds that
// own its live instances. vmmd itself fans that update out to all of its local
// instances, so schedd only sends one update per node.
type PrivateNetworkRouteApplier struct {
	store        state.Store
	router       PrivateNetworkRouter
	peeringStore state.PrivateNetworkPeeringReconcileStore
	log          *slog.Logger
}

// PrivateNetworkPeeringRouteApplier translates the provider-neutral peering
// route set into the existing app-netns route update path. Peering routes are
// derived state: the attachment's persisted CIDRs remain the base network
// routes, while every sweep replaces the effective set and therefore removes
// routes for deleted or blocked peerings as well.
type PrivateNetworkPeeringRouteApplier struct {
	attachments PrivateNetworkAttachmentState
	routes      *PrivateNetworkRouteApplier
	log         *slog.Logger
}

// PrivateNetworkAttachmentState is the combined store surface required by
// Gregale-owned peering route convergence.
type PrivateNetworkAttachmentState interface {
	state.Store
	state.AppPrivateNetworkAttachmentReconcileStore
}

func NewPrivateNetworkPeeringRouteApplier(store PrivateNetworkAttachmentState, router PrivateNetworkRouter, log *slog.Logger) *PrivateNetworkPeeringRouteApplier {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkPeeringRouteApplier{
		attachments: store,
		routes:      NewPrivateNetworkRouteApplier(store, router, log),
		log:         log,
	}
}

// NewPrivateNetworkPeeringRouteApplierWithRouteApplier shares the route
// applier used by the attachment reconciler. This is important because the
// attachment worker also replays ready rows; sharing the overlay keeps a
// peering route from being overwritten by a later base-CIDR replay.
func NewPrivateNetworkPeeringRouteApplierWithRouteApplier(store PrivateNetworkAttachmentState, routes *PrivateNetworkRouteApplier, log *slog.Logger) *PrivateNetworkPeeringRouteApplier {
	if log == nil {
		log = slog.Default()
	}
	if routes == nil {
		routes = NewPrivateNetworkRouteApplier(store, nil, log)
	}
	return &PrivateNetworkPeeringRouteApplier{attachments: store, routes: routes, log: log}
}

// Apply replaces all effective routes for attachments in one account and
// region. An empty routes slice is meaningful: it restores each attachment's
// base CIDRs and clears stale peering destinations from every live node.
func (a *PrivateNetworkPeeringRouteApplier) Apply(ctx context.Context, accountID, region string, routes []privatenetwork.PeeringRoute) error {
	if a == nil || a.attachments == nil || a.routes == nil {
		return errors.New("private network peering route applier is not configured")
	}
	attachments, err := a.attachments.ListAppPrivateNetworkAttachments(ctx, []string{api.PrivateNetworkAttachmentStatusReady}, 1000)
	if err != nil {
		return fmt.Errorf("list ready private network attachments: %w", err)
	}
	destinations := make(map[string][]netip.Prefix)
	for _, route := range routes {
		if !route.DestinationCIDR.IsValid() || route.FromNetworkID == "" {
			return fmt.Errorf("invalid private network peering route for %q", route.FromNetworkID)
		}
		destinations[route.FromNetworkID] = append(destinations[route.FromNetworkID], route.DestinationCIDR)
	}

	var errs []error
	for _, attachment := range attachments {
		if attachment.AccountID != accountID || attachment.Region != region {
			continue
		}
		effective := append([]netip.Prefix(nil), attachment.CIDRs...)
		effective = append(effective, destinations[attachment.NetworkID]...)
		effective = canonicalPrivateNetworkPrefixes(effective)
		attachment.CIDRs = effective
		if _, applyErr := a.routes.ApplyAttachmentWithReport(ctx, attachment); applyErr != nil {
			err := fmt.Errorf("app %s: %w", attachment.AppID, applyErr)
			errs = append(errs, err)
			a.log.Warn("schedd: private network peering routes update failed", "account", accountID, "region", region, "app", attachment.AppID, "err", applyErr)
		}
	}
	return errors.Join(errs...)
}

func canonicalPrivateNetworkPrefixes(prefixes []netip.Prefix) []netip.Prefix {
	seen := make(map[string]struct{}, len(prefixes))
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		if !prefix.IsValid() {
			continue
		}
		prefix = prefix.Masked()
		key := prefix.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, prefix)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func NewPrivateNetworkRouteApplier(store state.Store, router PrivateNetworkRouter, log *slog.Logger) *PrivateNetworkRouteApplier {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkRouteApplier{store: store, router: router, log: log}
}

// WithPeeringStore enables the ready-peering overlay for attachment replays.
// The overlay is optional so provider-only and legacy route-only deployments
// retain the original route applier behavior.
func (a *PrivateNetworkRouteApplier) WithPeeringStore(store state.PrivateNetworkPeeringReconcileStore) *PrivateNetworkRouteApplier {
	a.peeringStore = store
	return a
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
	return a.applyWithReport(ctx, appID, cidrs, nil, nil, "", netip.Addr{})
}

func (a *PrivateNetworkRouteApplier) ApplyAttachmentWithReport(ctx context.Context, attachment state.AppPrivateNetworkAttachment) (privatenetwork.RouteApplyReport, error) {
	if a.peeringStore != nil {
		effective, err := a.withReadyPeeringRoutes(ctx, attachment)
		if err != nil {
			return privatenetwork.RouteApplyReport{}, err
		}
		attachment = effective
	}
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
				return a.applyWithReport(ctx, attachment.AppID, attachment.CIDRs, attachment.AllowedCIDRs, attachment.FirewallRules, attachment.NetworkID, address.Address)
			} else if !errors.Is(err, state.ErrNotFound) {
				return privatenetwork.RouteApplyReport{}, fmt.Errorf("private network lookup: %w", err)
			}
		}
	}
	return a.applyWithReport(ctx, attachment.AppID, attachment.CIDRs, attachment.AllowedCIDRs, attachment.FirewallRules, "", netip.Addr{})
}

func (a *PrivateNetworkRouteApplier) withReadyPeeringRoutes(ctx context.Context, attachment state.AppPrivateNetworkAttachment) (state.AppPrivateNetworkAttachment, error) {
	networkStore, ok := a.store.(state.PrivateNetworkStore)
	if !ok {
		return state.AppPrivateNetworkAttachment{}, errors.New("private network peering route overlay requires private network state")
	}
	rows, err := a.peeringStore.ListPrivateNetworkPeeringsForReconcile(ctx, []string{api.PrivateNetworkPeeringStatusReady}, 1000)
	if err != nil {
		return state.AppPrivateNetworkAttachment{}, fmt.Errorf("list ready private network peerings: %w", err)
	}
	specs := make([]privatenetwork.PeeringSpec, 0, len(rows))
	for _, row := range rows {
		if row.AccountID != attachment.AccountID || row.Region != attachment.Region {
			continue
		}
		left, leftErr := networkStore.GetPrivateNetwork(ctx, row.AccountID, row.LeftNetworkID)
		right, rightErr := networkStore.GetPrivateNetwork(ctx, row.AccountID, row.RightNetworkID)
		if leftErr != nil || rightErr != nil {
			if leftErr != nil {
				return state.AppPrivateNetworkAttachment{}, fmt.Errorf("private network peering left network lookup: %w", leftErr)
			}
			return state.AppPrivateNetworkAttachment{}, fmt.Errorf("private network peering right network lookup: %w", rightErr)
		}
		specs = append(specs, privatenetwork.PeeringSpec{
			ID: row.ID, AccountID: row.AccountID, Region: row.Region,
			Left:  privatenetwork.FabricSpec{AccountID: left.AccountID, NetworkID: left.ID, Region: left.Region, CIDR: left.CIDR},
			Right: privatenetwork.FabricSpec{AccountID: right.AccountID, NetworkID: right.ID, Region: right.Region, CIDR: right.CIDR},
		})
	}
	if len(specs) == 0 {
		return attachment, nil
	}
	routes, err := privatenetwork.BuildPeeringRoutes(specs)
	if err != nil {
		return state.AppPrivateNetworkAttachment{}, fmt.Errorf("build ready private network peering routes: %w", err)
	}
	for _, route := range routes {
		if route.FromNetworkID == attachment.NetworkID {
			attachment.CIDRs = append(attachment.CIDRs, route.DestinationCIDR)
		}
	}
	attachment.CIDRs = canonicalPrivateNetworkPrefixes(attachment.CIDRs)
	return attachment, nil
}

func (a *PrivateNetworkRouteApplier) applyWithReport(ctx context.Context, appID string, cidrs, allowedCIDRs []netip.Prefix, firewallRules []api.PrivateNetworkFirewallRule, networkID string, address netip.Addr) (privatenetwork.RouteApplyReport, error) {
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
			if len(firewallRules) > 0 {
				policyRouter, ok := a.router.(PrivateNetworkFirewallPolicyAttachmentRouter)
				if !ok {
					applyErr = fmt.Errorf("private network firewall rules update unsupported")
				} else {
					applyErr = policyRouter.UpdatePrivateNetworkAttachmentWithFirewall(ctx, nodeID, appID, networkID, address, cidrs, allowedCIDRs, firewallRules)
				}
			} else if len(allowedCIDRs) > 0 {
				policyRouter, ok := a.router.(PrivateNetworkPolicyAttachmentRouter)
				if !ok {
					applyErr = fmt.Errorf("private network policy update unsupported")
				} else {
					applyErr = policyRouter.UpdatePrivateNetworkAttachmentWithPolicy(ctx, nodeID, appID, networkID, address, cidrs, allowedCIDRs)
				}
			} else {
				applyErr = a.router.(PrivateNetworkAttachmentRouter).UpdatePrivateNetworkAttachment(ctx, nodeID, appID, networkID, address, cidrs)
			}
		} else {
			if len(firewallRules) > 0 {
				policyRouter, ok := a.router.(PrivateNetworkFirewallPolicyRouter)
				if !ok {
					applyErr = fmt.Errorf("private network firewall rules update unsupported")
				} else {
					applyErr = policyRouter.UpdatePrivateNetworkWithFirewall(ctx, nodeID, appID, cidrs, allowedCIDRs, firewallRules)
				}
			} else if len(allowedCIDRs) > 0 {
				policyRouter, ok := a.router.(PrivateNetworkPolicyRouter)
				if !ok {
					applyErr = fmt.Errorf("private network policy update unsupported")
				} else {
					applyErr = policyRouter.UpdatePrivateNetworkWithPolicy(ctx, nodeID, appID, cidrs, allowedCIDRs)
				}
			} else {
				applyErr = a.router.UpdatePrivateNetwork(ctx, nodeID, appID, cidrs)
			}
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

// PrivateNetworkFabricTeardown fans a durable network deletion out to every
// active compute node in the network's region. The vmmd operation is
// idempotent, while the joined error keeps the outbox row pending until every
// node has acknowledged cleanup.
type PrivateNetworkFabricTeardown struct {
	store  state.Store
	router PrivateNetworkFabricTeardownRouter
	log    *slog.Logger
}

func NewPrivateNetworkFabricTeardown(store state.Store, router PrivateNetworkFabricTeardownRouter, log *slog.Logger) *PrivateNetworkFabricTeardown {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkFabricTeardown{store: store, router: router, log: log}
}

func (t *PrivateNetworkFabricTeardown) Remove(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix) error {
	if t == nil || t.store == nil || t.router == nil {
		return errors.New("private network fabric teardown is not configured")
	}
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(networkID) == "" || !cidr.IsValid() {
		return errors.New("private network fabric teardown requires account, network, and CIDR")
	}
	rows, err := t.store.ActiveComputeNodes(ctx)
	if err != nil {
		return fmt.Errorf("list active compute nodes: %w", err)
	}
	nodes := make([]state.ComputeNode, 0, len(rows))
	for _, node := range rows {
		if strings.TrimSpace(node.ID) == "" || !node.Active || !fabricNodeInRegion(node, region) {
			continue
		}
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	var errs []error
	for _, node := range nodes {
		if err := t.router.RemovePrivateNetworkFabric(ctx, node.ID, accountID, networkID, region, cidr); err != nil {
			err = fmt.Errorf("node %s: %w", node.ID, err)
			errs = append(errs, err)
			t.log.Warn("schedd: private network fabric teardown failed", "account", accountID, "network", networkID, "region", region, "node", node.ID, "err", err)
		}
	}
	return errors.Join(errs...)
}

// PrivateNetworkFabricDeletionSubscriber turns the durable network-deleted
// notification into node-local fabric cleanup. The network row is already
// gone, so the notification carries the immutable region and CIDR needed for
// teardown.
type PrivateNetworkFabricDeletionSubscriber struct {
	teardown *PrivateNetworkFabricTeardown
	log      *slog.Logger
}

func NewPrivateNetworkFabricDeletionSubscriber(teardown *PrivateNetworkFabricTeardown, log *slog.Logger) *PrivateNetworkFabricDeletionSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkFabricDeletionSubscriber{teardown: teardown, log: log}
}

func (s *PrivateNetworkFabricDeletionSubscriber) Handle(ctx context.Context, n db.Notification) error {
	if n.Channel != db.NotifyPrivateNetworkChanged {
		return nil
	}
	var payload struct {
		Kind      string `json:"kind"`
		AccountID string `json:"account_id"`
		NetworkID string `json:"network_id"`
		Region    string `json:"region"`
		CIDR      string `json:"cidr"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		return fmt.Errorf("decode private network deletion: %w", err)
	}
	if payload.Kind != "private_network_deleted" {
		return nil
	}
	if strings.TrimSpace(payload.AccountID) == "" || strings.TrimSpace(payload.NetworkID) == "" || strings.TrimSpace(payload.Region) == "" {
		return errors.New("private network deletion requires account_id, network_id, and region")
	}
	cidr, err := api.ValidatePrivateNetworkCIDR(payload.CIDR)
	if err != nil {
		return fmt.Errorf("private network deletion CIDR: %w", err)
	}
	if s.teardown == nil {
		return errors.New("private network fabric deletion subscriber is not configured")
	}
	if err := s.teardown.Remove(ctx, payload.AccountID, payload.NetworkID, payload.Region, cidr); err != nil {
		return err
	}
	s.log.Debug("schedd: private network fabric teardown converged", "account", payload.AccountID, "network", payload.NetworkID, "region", payload.Region)
	return nil
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

// PrivateNetworkPeeringSweeper is the event-driven subset of the durable
// peering reconciler. Keeping this seam small makes notification handling
// testable without coupling schedd's loop to the reconciler implementation.
type PrivateNetworkPeeringSweeper interface {
	SweepAccountRegion(context.Context, string, string) (privatenetwork.PeeringReconcileSummary, error)
}

// PrivateNetworkPolicySweeper is the event-driven subset of the attachment
// reconciler. Network policy changes target one account/network and leave the
// periodic global sweep as the recovery path for missed notifications.
type PrivateNetworkPolicySweeper interface {
	SweepNetwork(context.Context, string, string) (privatenetwork.ReconcileSummary, error)
}

// PrivateNetworkPolicySubscriber turns durable network policy mutations into
// immediate attachment convergence across the affected live workloads.
type PrivateNetworkPolicySubscriber struct {
	sweeper PrivateNetworkPolicySweeper
	log     *slog.Logger
}

func NewPrivateNetworkPolicySubscriber(sweeper PrivateNetworkPolicySweeper, log *slog.Logger) *PrivateNetworkPolicySubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkPolicySubscriber{sweeper: sweeper, log: log}
}

func (s *PrivateNetworkPolicySubscriber) Handle(ctx context.Context, n db.Notification) error {
	if n.Channel != db.NotifyPrivateNetworkChanged {
		return nil
	}
	var payload struct {
		Kind      string `json:"kind"`
		AccountID string `json:"account_id"`
		NetworkID string `json:"network_id"`
		Region    string `json:"region"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		return fmt.Errorf("decode private network policy change: %w", err)
	}
	if payload.Kind != "private_network" {
		return nil
	}
	if strings.TrimSpace(payload.AccountID) == "" || strings.TrimSpace(payload.NetworkID) == "" {
		return errors.New("private network policy change requires account_id and network_id")
	}
	if s.sweeper == nil {
		return errors.New("private network policy subscriber is not configured")
	}
	if _, err := s.sweeper.SweepNetwork(ctx, payload.AccountID, payload.NetworkID); err != nil {
		return err
	}
	s.log.Debug("schedd: private network policy mutation converged", "account", payload.AccountID, "network", payload.NetworkID, "region", payload.Region, "status", payload.Status)
	return nil
}

// PrivateNetworkPeeringSubscriber turns network mutation notifications into
// immediate account/region convergence. The periodic reconciler remains the
// safety net when a notification is missed.
type PrivateNetworkPeeringSubscriber struct {
	sweeper PrivateNetworkPeeringSweeper
	log     *slog.Logger
}

func NewPrivateNetworkPeeringSubscriber(sweeper PrivateNetworkPeeringSweeper, log *slog.Logger) *PrivateNetworkPeeringSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &PrivateNetworkPeeringSubscriber{sweeper: sweeper, log: log}
}

func (s *PrivateNetworkPeeringSubscriber) Handle(ctx context.Context, n db.Notification) error {
	if n.Channel != db.NotifyPrivateNetworkChanged {
		return nil
	}
	var payload struct {
		Kind      string `json:"kind"`
		AccountID string `json:"account_id"`
		Region    string `json:"region"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		return fmt.Errorf("decode private network change: %w", err)
	}
	if payload.Kind != "private_network_peering" {
		return nil
	}
	if strings.TrimSpace(payload.AccountID) == "" || strings.TrimSpace(payload.Region) == "" {
		return errors.New("private network change requires account_id and region")
	}
	if s.sweeper == nil {
		return errors.New("private network peering subscriber is not configured")
	}
	if _, err := s.sweeper.SweepAccountRegion(ctx, payload.AccountID, payload.Region); err != nil {
		return err
	}
	s.log.Debug("schedd: private network peering mutation converged", "account", payload.AccountID, "region", payload.Region, "status", payload.Status)
	return nil
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
