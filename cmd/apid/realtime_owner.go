package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const managedRealtimeOwnerLeaseTTL = 30 * time.Second
const maxConcurrentRealtimePublishes = 8
const managedRealtimePartialPublishLogInterval = time.Minute

const (
	managedRealtimePublishOp     = "managed_realtime_publish"
	managedRealtimePublishNodeOp = "managed_realtime_publish_node"
)

var errManagedRealtimeOwnerUnavailable = errors.New("realtime: owner node unavailable")

// realtimeNodeOperator is the node-local management contract plus the
// endpoint/registry operations used by the fleet registrar.
type realtimeNodeOperator interface {
	realtimeOwner
	Connections(context.Context) ([]realtime.ConnectionInfo, error)
	Endpoints(context.Context) ([]string, error)
	ChannelRoutes(context.Context) ([]realtime.ChannelRoute, error)
	RegisterEndpoint(context.Context, realtime.Endpoint) error
	RemoveEndpoint(context.Context, string) error
}

type realtimeRouteAwareUnsubscriber interface {
	UnsubscribeWithRouteState(context.Context, string, string, string) (hasSubscribers bool, known bool, err error)
}

type localRealtimeNodeOperator struct {
	owner  realtimeOwner
	client *realtime.Client
}

func (o localRealtimeNodeOperator) Send(ctx context.Context, endpointID, connectionID string, message realtime.Message) error {
	return o.owner.Send(ctx, endpointID, connectionID, message)
}
func (o localRealtimeNodeOperator) CloseConnection(ctx context.Context, endpointID, connectionID, reason string) error {
	return o.owner.CloseConnection(ctx, endpointID, connectionID, reason)
}
func (o localRealtimeNodeOperator) Subscribe(ctx context.Context, endpointID, connectionID, channel string) error {
	return o.owner.Subscribe(ctx, endpointID, connectionID, channel)
}
func (o localRealtimeNodeOperator) Unsubscribe(ctx context.Context, endpointID, connectionID, channel string) error {
	return o.owner.Unsubscribe(ctx, endpointID, connectionID, channel)
}
func (o localRealtimeNodeOperator) UnsubscribeWithRouteState(ctx context.Context, endpointID, connectionID, channel string) (bool, bool, error) {
	if unsubscriber, ok := o.owner.(realtimeRouteAwareUnsubscriber); ok {
		return unsubscriber.UnsubscribeWithRouteState(ctx, endpointID, connectionID, channel)
	}
	return false, false, o.owner.Unsubscribe(ctx, endpointID, connectionID, channel)
}
func (o localRealtimeNodeOperator) Publish(ctx context.Context, endpointID, channel string, message realtime.Message) (int, error) {
	return o.owner.Publish(ctx, endpointID, channel, message)
}
func (o localRealtimeNodeOperator) Connections(ctx context.Context) ([]realtime.ConnectionInfo, error) {
	return o.client.Connections(ctx)
}
func (o localRealtimeNodeOperator) Endpoints(ctx context.Context) ([]string, error) {
	return o.client.Endpoints(ctx)
}
func (o localRealtimeNodeOperator) ChannelRoutes(ctx context.Context) ([]realtime.ChannelRoute, error) {
	return o.client.ChannelRoutes(ctx)
}
func (o localRealtimeNodeOperator) ChannelRouteRevision(ctx context.Context) (realtime.ChannelRouteRevision, error) {
	return o.client.ChannelRouteRevision(ctx)
}
func (o localRealtimeNodeOperator) RegisterEndpoint(ctx context.Context, endpoint realtime.Endpoint) error {
	return o.client.RegisterEndpoint(ctx, endpoint)
}
func (o localRealtimeNodeOperator) RemoveEndpoint(ctx context.Context, endpointID string) error {
	return o.client.RemoveEndpoint(ctx, endpointID)
}

// ListConnectionInventory returns the local snapshot in the same shape used
// by the fleet resolver. The public handler can therefore use the same read
// path in single-box and split-node deployments.
func (o localRealtimeOwner) ListConnectionInventory(ctx context.Context) (realtime.ConnectionInventory, error) {
	connections, err := o.client.Connections(ctx)
	if err != nil {
		return realtime.ConnectionInventory{}, err
	}
	return realtime.ConnectionInventory{Connections: connections, NodesQueried: 1}, nil
}

type remoteRealtimeNodeOperator struct{ client *realtime.Client }

func (o remoteRealtimeNodeOperator) Send(ctx context.Context, _, connectionID string, message realtime.Message) error {
	return o.client.Send(ctx, connectionID, message)
}
func (o remoteRealtimeNodeOperator) CloseConnection(ctx context.Context, _, connectionID, reason string) error {
	return o.client.CloseConnection(ctx, connectionID, reason)
}
func (o remoteRealtimeNodeOperator) Subscribe(ctx context.Context, _, connectionID, channel string) error {
	return o.client.Subscribe(ctx, connectionID, channel)
}
func (o remoteRealtimeNodeOperator) Unsubscribe(ctx context.Context, _, connectionID, channel string) error {
	return o.client.Unsubscribe(ctx, connectionID, channel)
}
func (o remoteRealtimeNodeOperator) UnsubscribeWithRouteState(ctx context.Context, _, connectionID, channel string) (bool, bool, error) {
	return o.client.UnsubscribeWithRouteState(ctx, connectionID, channel)
}
func (o remoteRealtimeNodeOperator) Publish(ctx context.Context, endpointID, channel string, message realtime.Message) (int, error) {
	return o.client.Publish(ctx, endpointID, channel, message)
}
func (o remoteRealtimeNodeOperator) Connections(ctx context.Context) ([]realtime.ConnectionInfo, error) {
	return o.client.Connections(ctx)
}
func (o remoteRealtimeNodeOperator) Endpoints(ctx context.Context) ([]string, error) {
	return o.client.Endpoints(ctx)
}
func (o remoteRealtimeNodeOperator) ChannelRoutes(ctx context.Context) ([]realtime.ChannelRoute, error) {
	return o.client.ChannelRoutes(ctx)
}
func (o remoteRealtimeNodeOperator) ChannelRouteRevision(ctx context.Context) (realtime.ChannelRouteRevision, error) {
	return o.client.ChannelRouteRevision(ctx)
}
func (o remoteRealtimeNodeOperator) RegisterEndpoint(ctx context.Context, endpoint realtime.Endpoint) error {
	return o.client.RegisterEndpoint(ctx, endpoint)
}
func (o remoteRealtimeNodeOperator) RemoveEndpoint(ctx context.Context, endpointID string) error {
	return o.client.RemoveEndpoint(ctx, endpointID)
}

// leasedRealtimeOwner resolves a connection through the durable owner
// directory. A missing/expired row is repaired by probing each active node,
// then claiming a short lease with a CAS token. The registry is a hint with a
// bounded lifetime: a crashed node cannot strand a connection forever, while
// a live connection avoids a fleet-wide probe on every API call.
type leasedRealtimeOwner struct {
	registry state.ManagedRealtimeConnectionOwnerStore
	nodes    interface {
		ActiveComputeNodes(context.Context) ([]state.ComputeNode, error)
		ComputeNodeByID(context.Context, string) (state.ComputeNode, error)
	}
	localNodeID               string
	local                     realtimeNodeOperator
	log                       *slog.Logger
	ops                       *wire.OpsMetrics
	channelRouteMetrics       *managedRealtimeChannelRouteMetrics
	channelRoutingEnabled     bool
	channelRoutes             state.ManagedRealtimeChannelRouteStore
	clientFor                 func(state.ComputeNode) (realtimeNodeOperator, error)
	leaseTTL                  time.Duration
	publishWarnMu             sync.Mutex
	lastPartialPublishWarning time.Time
	routeRevisionMu           sync.Mutex
	routeRevisions            map[string]realtime.ChannelRouteRevision
}

func newLeasedRealtimeOwner(registry state.ManagedRealtimeConnectionOwnerStore, nodes interface {
	ActiveComputeNodes(context.Context) ([]state.ComputeNode, error)
	ComputeNodeByID(context.Context, string) (state.ComputeNode, error)
}, localNodeID string, local realtimeNodeOperator, log *slog.Logger) *leasedRealtimeOwner {
	if log == nil {
		log = slog.Default()
	}
	return &leasedRealtimeOwner{
		registry: registry, nodes: nodes, localNodeID: localNodeID,
		local: local, log: log, leaseTTL: managedRealtimeOwnerLeaseTTL,
		clientFor: defaultRealtimeNodeOperator,
	}
}

func (o *leasedRealtimeOwner) nodeOperator(node state.ComputeNode) (realtimeNodeOperator, error) {
	if node.ID == o.localNodeID && o.local != nil {
		return o.local, nil
	}
	if o.clientFor == nil {
		return nil, errManagedRealtimeOwnerUnavailable
	}
	return o.clientFor(node)
}

func (o *leasedRealtimeOwner) routeRevision(nodeID string) (realtime.ChannelRouteRevision, bool) {
	o.routeRevisionMu.Lock()
	defer o.routeRevisionMu.Unlock()
	revision, ok := o.routeRevisions[nodeID]
	return revision, ok
}

func (o *leasedRealtimeOwner) setRouteRevision(nodeID string, revision realtime.ChannelRouteRevision) {
	o.routeRevisionMu.Lock()
	defer o.routeRevisionMu.Unlock()
	if o.routeRevisions == nil {
		o.routeRevisions = make(map[string]realtime.ChannelRouteRevision)
	}
	o.routeRevisions[nodeID] = revision
}

func (o *leasedRealtimeOwner) discover(ctx context.Context, endpointID, connectionID string) (state.ManagedRealtimeConnectionOwner, realtimeNodeOperator, error) {
	if o.nodes == nil {
		return state.ManagedRealtimeConnectionOwner{}, nil, errManagedRealtimeOwnerUnavailable
	}
	nodes, err := o.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return state.ManagedRealtimeConnectionOwner{}, nil, fmt.Errorf("%w: list active nodes: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	var successful int
	var lastErr error
	for _, node := range nodes {
		op, err := o.nodeOperator(node)
		if err != nil {
			lastErr = err
			continue
		}
		connections, err := op.Connections(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		successful++
		found := false
		for _, connection := range connections {
			if connection.ID == connectionID && connection.EndpointID == endpointID {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		lease, err := o.registry.ClaimManagedRealtimeConnectionOwner(ctx, connectionID, endpointID, node.ID, o.leaseTTL)
		if errors.Is(err, state.ErrManagedRealtimeOwnerConflict) {
			// Another API replica won the claim. Re-read immediately; it
			// may point at this same node or at a still-live peer. This
			// closes the race where discovery would otherwise return a
			// misleading 410 to the losing API request.
			if current, getErr := o.registry.GetManagedRealtimeConnectionOwner(ctx, connectionID, endpointID); getErr == nil && o.nodes != nil {
				if currentNode, nodeErr := o.nodes.ComputeNodeByID(ctx, current.NodeID); nodeErr == nil && currentNode.Active {
					if currentOp, opErr := o.nodeOperator(currentNode); opErr == nil {
						if _, renewErr := o.registry.RenewManagedRealtimeConnectionOwner(ctx, connectionID, endpointID, current.LeaseToken, o.leaseTTL); renewErr == nil {
							return current, currentOp, nil
						}
					}
				}
			}
			continue
		}
		if err != nil {
			return state.ManagedRealtimeConnectionOwner{}, nil, err
		}
		return lease, op, nil
	}
	// A partial inventory cannot prove absence: the connection may live on
	// the node that did not answer. Preserve a retryable availability error.
	if lastErr != nil {
		return state.ManagedRealtimeConnectionOwner{}, nil, fmt.Errorf("%w: incomplete owner discovery: %w", errManagedRealtimeOwnerUnavailable, lastErr)
	}
	if successful > 0 {
		return state.ManagedRealtimeConnectionOwner{}, nil, realtime.ErrConnectionNotFound
	}
	if lastErr == nil {
		lastErr = errors.New("no active realtime nodes responded")
	}
	return state.ManagedRealtimeConnectionOwner{}, nil, fmt.Errorf("%w: %w", errManagedRealtimeOwnerUnavailable, lastErr)
}

func (o *leasedRealtimeOwner) resolve(ctx context.Context, endpointID, connectionID string) (state.ManagedRealtimeConnectionOwner, realtimeNodeOperator, error) {
	if o.registry == nil {
		return state.ManagedRealtimeConnectionOwner{}, nil, errManagedRealtimeOwnerUnavailable
	}
	if lease, err := o.registry.GetManagedRealtimeConnectionOwner(ctx, connectionID, endpointID); err == nil {
		if o.nodes != nil {
			if node, nodeErr := o.nodes.ComputeNodeByID(ctx, lease.NodeID); nodeErr == nil && node.Active {
				if op, opErr := o.nodeOperator(node); opErr == nil {
					if _, renewErr := o.registry.RenewManagedRealtimeConnectionOwner(ctx, connectionID, endpointID, lease.LeaseToken, o.leaseTTL); renewErr == nil {
						return lease, op, nil
					}
				}
			}
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		return state.ManagedRealtimeConnectionOwner{}, nil, fmt.Errorf("%w: read directory: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	return o.discover(ctx, endpointID, connectionID)
}

func (o *leasedRealtimeOwner) connectionOperation(ctx context.Context, endpointID, connectionID string, closeAfter bool, fn func(state.ManagedRealtimeConnectionOwner, realtimeNodeOperator) error) error {
	for attempt := 0; attempt < 2; attempt++ {
		lease, op, err := o.resolve(ctx, endpointID, connectionID)
		if err != nil {
			return err
		}
		err = fn(lease, op)
		if err == nil {
			if closeAfter {
				_ = o.registry.ReleaseManagedRealtimeConnectionOwner(ctx, connectionID, lease.LeaseToken)
			}
			return nil
		}
		var managementErr *realtime.ManagementError
		if errors.Is(err, realtime.ErrConnectionNotFound) || (errors.As(err, &managementErr) && managementErr.StatusCode == http.StatusNotFound) {
			_ = o.registry.ReleaseManagedRealtimeConnectionOwner(ctx, connectionID, lease.LeaseToken)
			if attempt == 0 {
				continue
			}
		}
		return err
	}
	return errManagedRealtimeOwnerUnavailable
}

func (o *leasedRealtimeOwner) Send(ctx context.Context, endpointID, connectionID string, message realtime.Message) error {
	return o.connectionOperation(ctx, endpointID, connectionID, false, func(_ state.ManagedRealtimeConnectionOwner, op realtimeNodeOperator) error {
		return op.Send(ctx, endpointID, connectionID, message)
	})
}

func (o *leasedRealtimeOwner) CloseConnection(ctx context.Context, endpointID, connectionID, reason string) error {
	return o.connectionOperation(ctx, endpointID, connectionID, true, func(_ state.ManagedRealtimeConnectionOwner, op realtimeNodeOperator) error {
		return op.CloseConnection(ctx, endpointID, connectionID, reason)
	})
}

func (o *leasedRealtimeOwner) Subscribe(ctx context.Context, endpointID, connectionID, channel string) error {
	return o.connectionOperation(ctx, endpointID, connectionID, false, func(lease state.ManagedRealtimeConnectionOwner, op realtimeNodeOperator) error {
		if o.channelRoutes != nil {
			lock, err := o.channelRoutes.AcquireManagedRealtimeChannelRouteLock(ctx, lease.NodeID)
			if err != nil {
				return fmt.Errorf("realtime: lock channel route snapshot before subscribe: %w", err)
			}
			defer lock.Release(ctx)
			if err := o.channelRoutes.AddManagedRealtimeChannelRoutes(ctx, []state.ManagedRealtimeChannelRoute{{
				EndpointID: endpointID, Channel: channel, NodeID: lease.NodeID,
			}}); err != nil {
				// A new subscriber is never accepted while the shared routing
				// hint cannot be committed; another apid may already have marked
				// this node's snapshot complete.
				return fmt.Errorf("realtime: record channel route before subscribe: %w", err)
			}
		}
		return op.Subscribe(ctx, endpointID, connectionID, channel)
	})
}

func (o *leasedRealtimeOwner) Unsubscribe(ctx context.Context, endpointID, connectionID, channel string) error {
	return o.connectionOperation(ctx, endpointID, connectionID, false, func(lease state.ManagedRealtimeConnectionOwner, op realtimeNodeOperator) error {
		var lock state.ManagedRealtimeChannelRouteLock
		canRemoveRoute := false
		if o.channelRoutes != nil {
			var err error
			lock, err = o.channelRoutes.AcquireManagedRealtimeChannelRouteLock(ctx, lease.NodeID)
			if err != nil {
				// Route cleanup is an optimization. Keep the positive hint and
				// allow the unsubscribe if its coordination lock is unavailable.
				return op.Unsubscribe(ctx, endpointID, connectionID, channel)
			}
			defer lock.Release(ctx)
			canRemoveRoute = true
		}

		var hasSubscribers, known bool
		var err error
		if unsubscriber, ok := op.(realtimeRouteAwareUnsubscriber); ok {
			hasSubscribers, known, err = unsubscriber.UnsubscribeWithRouteState(ctx, endpointID, connectionID, channel)
		} else {
			err = op.Unsubscribe(ctx, endpointID, connectionID, channel)
		}
		if err != nil {
			return err
		}
		if !known || hasSubscribers || o.channelRoutes == nil || !canRemoveRoute {
			return nil
		}
		remover, ok := o.channelRoutes.(state.ManagedRealtimeChannelRouteRemover)
		if !ok {
			return nil
		}
		if err := remover.RemoveManagedRealtimeChannelRoute(ctx, state.ManagedRealtimeChannelRoute{
			EndpointID: endpointID, Channel: channel, NodeID: lease.NodeID,
		}); err != nil {
			logger := o.log
			if logger == nil {
				logger = slog.Default()
			}
			logger.WarnContext(ctx, "failed to remove empty realtime channel route; retaining route hint", "node_id", lease.NodeID, "error", err)
		}
		return nil
	})
}

func (o *leasedRealtimeOwner) publishTargetNodes(ctx context.Context, endpointID, channel string) ([]state.ComputeNode, bool, error) {
	if o.channelRoutingEnabled && o.channelRoutes != nil {
		if targetStore, ok := o.channelRoutes.(state.ManagedRealtimeChannelPublishTargetStore); ok {
			view, err := targetStore.ListManagedRealtimeChannelPublishTargets(ctx, endpointID, channel)
			if err != nil {
				// Keep the directory as an optimization: a failed read falls back
				// to the same full-fleet publish used before route indexing.
				nodes, activeErr := o.nodes.ActiveComputeNodes(ctx)
				if activeErr != nil {
					return nil, false, activeErr
				}
				if len(nodes) > 0 {
					o.channelRouteMetrics.publish("directory_error", len(nodes))
				}
				return nodes, false, nil
			}
			if !view.HasActiveNodes {
				return nil, false, nil
			}

			nodes := make([]state.ComputeNode, 0, len(view.Targets))
			hasUnreadyNode := false
			for _, target := range view.Targets {
				nodes = append(nodes, state.ComputeNode{
					ID: target.NodeID, Name: target.NodeName, GatewayTargetURL: target.GatewayTargetURL,
				})
				if !target.SnapshotReady {
					hasUnreadyNode = true
				}
			}
			decision := "targeted"
			switch {
			case view.Disabled:
				decision = "overflow"
			case len(nodes) == 0:
				decision = "no_subscribers"
			case hasUnreadyNode:
				decision = "unready_fallback"
			}
			o.channelRouteMetrics.publish(decision, len(nodes))
			return nodes, len(nodes) == 0, nil
		}
	}

	nodes, err := o.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return nil, false, err
	}
	if len(nodes) > 0 {
		nodes = o.publishRecipients(ctx, endpointID, channel, nodes)
		if len(nodes) == 0 {
			return nil, true, nil
		}
	}
	return nodes, false, nil
}

// Publish routes to known subscriber nodes when shared channel hints are
// ready, while retaining fleet broadcast for unready nodes or degraded index
// reads. A node that is down is tolerated when at least one recipient accepts
// the request; if every recipient is unavailable the caller receives a 503.
func (o *leasedRealtimeOwner) Publish(ctx context.Context, endpointID, channel string, message realtime.Message) (int, error) {
	result, err := o.PublishWithStatus(ctx, endpointID, channel, message)
	return result.Queued, err
}

// PublishWithStatus reports partial fleet delivery. A successful node may
// have queued messages even when another is unavailable, so returning only
// an error would invite duplicate sends on a blind retry.
func (o *leasedRealtimeOwner) PublishWithStatus(ctx context.Context, endpointID, channel string, message realtime.Message) (api.ManagedRealtimePublishResponse, error) {
	var result api.ManagedRealtimePublishResponse
	started := time.Now()
	if o.nodes == nil {
		o.observePublish("unavailable", started)
		return result, errManagedRealtimeOwnerUnavailable
	}
	nodes, noSubscribers, err := o.publishTargetNodes(ctx, endpointID, channel)
	if err != nil {
		outcome := "unavailable"
		if ctx.Err() != nil {
			outcome = "canceled"
		}
		o.observePublish(outcome, started)
		return result, fmt.Errorf("%w: list active nodes: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	if noSubscribers {
		o.observePublish("no_subscribers", started)
		return result, nil
	}
	// Keep one result per node so aggregation and error selection stay in
	// fleet order even when node requests finish in a different order.
	type publishResult struct {
		attempted bool
		count     int
		err       error
		duration  time.Duration
	}
	results := make([]publishResult, len(nodes))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(len(nodes), maxConcurrentRealtimePublishes) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				results[i].attempted = true
				nodeStarted := time.Now()
				if err := ctx.Err(); err != nil {
					results[i].err = err
					results[i].duration = time.Since(nodeStarted)
					continue
				}
				op, err := o.nodeOperator(nodes[i])
				if err != nil {
					results[i].err = err
					results[i].duration = time.Since(nodeStarted)
					continue
				}
				results[i].count, results[i].err = op.Publish(ctx, endpointID, channel, message)
				results[i].duration = time.Since(nodeStarted)
			}
		}()
	}
dispatch:
	for i := range nodes {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- i:
		}
	}
	close(jobs)
	workers.Wait()

	attempted, endpointMissing, failed, canceled := 0, 0, 0, 0
	var lastErr error
	for _, outcome := range results {
		if !outcome.attempted {
			result.NodesUnavailable++
			continue
		}
		attempted++
		nodeOutcome := managedRealtimePublishNodeOutcome(outcome.err)
		if o.ops != nil {
			o.ops.ObserveCode(managedRealtimePublishNodeOp, nodeOutcome, outcome.duration)
		}
		switch nodeOutcome {
		case "endpoint_missing":
			endpointMissing++
			result.NodesUnavailable++
			// Endpoint registration can lag node activation; continue
			// without turning a healthy partial broadcast into a 503.
			continue
		case "canceled":
			canceled++
			lastErr = outcome.err
			result.NodesUnavailable++
			continue
		case "error":
			failed++
			lastErr = outcome.err
			result.NodesUnavailable++
			continue
		}
		result.NodesQueried++
		result.Queued += outcome.count
	}
	result.Partial = result.NodesUnavailable > 0
	if result.NodesQueried > 0 {
		outcome := "ok"
		if attempted < len(nodes) || endpointMissing > 0 || failed > 0 || canceled > 0 {
			outcome = "partial"
			o.warnPartialPublish(len(nodes), attempted, result.NodesQueried, endpointMissing, failed, canceled, result.Queued, time.Since(started))
		}
		o.observePublish(outcome, started)
		return result, nil
	}
	outcome := "unavailable"
	if ctx.Err() != nil {
		lastErr = ctx.Err()
		outcome = "canceled"
	}
	o.observePublish(outcome, started)
	if lastErr == nil {
		lastErr = errors.New("no active realtime nodes accepted publish")
	}
	return result, fmt.Errorf("%w: %w", errManagedRealtimeOwnerUnavailable, lastErr)
}

// publishRecipients narrows fanout only for nodes whose shared connection
// snapshot is current. An unready node is always sent the message, while
// stale positive route rows merely cause extra publishes.
func (o *leasedRealtimeOwner) publishRecipients(ctx context.Context, endpointID, channel string, active []state.ComputeNode) []state.ComputeNode {
	if !o.channelRoutingEnabled {
		o.channelRouteMetrics.publish("routing_disabled", len(active))
		return active
	}
	if o.channelRoutes == nil {
		o.channelRouteMetrics.publish("route_store_unavailable", len(active))
		return active
	}
	view, err := o.channelRoutes.ListManagedRealtimeChannelRouteView(ctx, endpointID, channel)
	if err != nil {
		o.channelRouteMetrics.publish("directory_error", len(active))
		return active
	}
	if view.Disabled {
		o.channelRouteMetrics.publish("overflow", len(active))
		return active
	}
	targets := make(map[string]struct{}, len(view.NodeIDs))
	for _, nodeID := range view.NodeIDs {
		targets[nodeID] = struct{}{}
	}
	ready := make(map[string]struct{}, len(view.ReadyNodeIDs))
	for _, nodeID := range view.ReadyNodeIDs {
		ready[nodeID] = struct{}{}
	}
	recipients := make([]state.ComputeNode, 0, len(active))
	hasUnreadyNode := false
	for _, node := range active {
		_, hasSubscriber := targets[node.ID]
		_, snapshotReady := ready[node.ID]
		if hasSubscriber || !snapshotReady {
			recipients = append(recipients, node)
		}
		if !snapshotReady {
			hasUnreadyNode = true
		}
	}
	decision := "targeted"
	if len(recipients) == 0 {
		decision = "no_subscribers"
	} else if hasUnreadyNode {
		decision = "unready_fallback"
	}
	o.channelRouteMetrics.publish(decision, len(recipients))
	return recipients
}

func managedRealtimePublishNodeOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	var managementErr *realtime.ManagementError
	if errors.As(err, &managementErr) && managementErr.StatusCode == http.StatusNotFound {
		return "endpoint_missing"
	}
	return "error"
}

func (o *leasedRealtimeOwner) observePublish(outcome string, started time.Time) {
	if o.ops != nil {
		o.ops.ObserveCode(managedRealtimePublishOp, outcome, time.Since(started))
	}
}

func (o *leasedRealtimeOwner) warnPartialPublish(total, attempted, accepted, endpointMissing, failed, canceled, queued int, duration time.Duration) {
	now := time.Now()
	o.publishWarnMu.Lock()
	if now.Sub(o.lastPartialPublishWarning) < managedRealtimePartialPublishLogInterval {
		o.publishWarnMu.Unlock()
		return
	}
	o.lastPartialPublishWarning = now
	o.publishWarnMu.Unlock()

	o.log.Warn("realtime: partial fleet publish",
		"nodes_total", total,
		"nodes_attempted", attempted,
		"nodes_accepted", accepted,
		"nodes_endpoint_missing", endpointMissing,
		"nodes_failed", failed,
		"nodes_canceled", canceled,
		"queued", queued,
		"duration", duration,
	)
}

// ListConnectionInventory aggregates point-in-time snapshots from active
// realtime nodes. A node that cannot be reached does not erase healthy
// results; the response carries the unavailable count so callers can decide
// whether to retry before taking action on an incomplete view.
func (o *leasedRealtimeOwner) ListConnectionInventory(ctx context.Context) (realtime.ConnectionInventory, error) {
	var inventory realtime.ConnectionInventory
	if o.nodes == nil {
		return inventory, errManagedRealtimeOwnerUnavailable
	}
	nodes, err := o.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return inventory, fmt.Errorf("%w: list active nodes: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	for _, node := range nodes {
		op, err := o.nodeOperator(node)
		if err != nil {
			inventory.NodesUnavailable++
			continue
		}
		connections, err := op.Connections(ctx)
		if err != nil {
			inventory.NodesUnavailable++
			continue
		}
		inventory.NodesQueried++
		inventory.Connections = append(inventory.Connections, connections...)
	}
	if inventory.NodesQueried == 0 && inventory.NodesUnavailable > 0 {
		return inventory, fmt.Errorf("%w: no active realtime nodes responded", errManagedRealtimeOwnerUnavailable)
	}
	return inventory, nil
}

// ListEndpointInventory reads the registrations actually present on active
// nodes. The reconciler uses it to repair deletions whose immediate fan-out
// missed a node, including after that node later rejoins the fleet.
func (o *leasedRealtimeOwner) ListEndpointInventory(ctx context.Context) (realtime.EndpointInventory, error) {
	var inventory realtime.EndpointInventory
	if o.nodes == nil {
		return inventory, errManagedRealtimeOwnerUnavailable
	}
	nodes, err := o.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return inventory, fmt.Errorf("%w: list active nodes: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	seen := make(map[string]struct{})
	for _, node := range nodes {
		op, err := o.nodeOperator(node)
		if err == nil {
			var ids []string
			ids, err = op.Endpoints(ctx)
			if err == nil {
				inventory.NodesQueried++
				for _, id := range ids {
					seen[id] = struct{}{}
				}
				continue
			}
		}
		inventory.NodesUnavailable++
	}
	for id := range seen {
		inventory.IDs = append(inventory.IDs, id)
	}
	sort.Strings(inventory.IDs)
	if inventory.NodesQueried == 0 && inventory.NodesUnavailable > 0 {
		return inventory, fmt.Errorf("%w: no active realtime nodes returned endpoints", errManagedRealtimeOwnerUnavailable)
	}
	return inventory, nil
}

// RegisterEndpoint and RemoveEndpoint fan out durable endpoint state to every
// active realtime node. The control-plane row remains authoritative; a node
// restart is repaired by the periodic endpoint reconciler.
func (o *leasedRealtimeOwner) RegisterEndpoint(ctx context.Context, endpoint realtime.Endpoint) error {
	return o.broadcastEndpoint(ctx, endpoint.ID, func(op realtimeNodeOperator) error {
		return op.RegisterEndpoint(ctx, endpoint)
	})
}

func (o *leasedRealtimeOwner) RemoveEndpoint(ctx context.Context, endpointID string) error {
	return o.broadcastEndpoint(ctx, endpointID, func(op realtimeNodeOperator) error {
		return op.RemoveEndpoint(ctx, endpointID)
	})
}

func (o *leasedRealtimeOwner) broadcastEndpoint(ctx context.Context, endpointID string, fn func(realtimeNodeOperator) error) error {
	if o.nodes == nil {
		return errManagedRealtimeOwnerUnavailable
	}
	nodes, err := o.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return fmt.Errorf("%w: list active nodes: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	var firstErr error
	reached := 0
	for _, node := range nodes {
		op, err := o.nodeOperator(node)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := fn(op); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		reached++
	}
	if reached > 0 {
		return nil
	}
	if firstErr == nil {
		firstErr = errors.New("no active realtime nodes accepted endpoint")
	}
	return fmt.Errorf("%w: endpoint %s: %w", errManagedRealtimeOwnerUnavailable, endpointID, firstErr)
}

func defaultRealtimeNodeOperator(node state.ComputeNode) (realtimeNodeOperator, error) {
	if node.GatewayTargetURL == nil || strings.TrimSpace(*node.GatewayTargetURL) == "" {
		return nil, fmt.Errorf("%w: node %s has no gateway_target_url", errManagedRealtimeOwnerUnavailable, node.Name)
	}
	base, err := realtimeGatewayBaseURL(*node.GatewayTargetURL)
	if err != nil {
		return nil, err
	}
	client := &realtime.Client{BaseURL: base, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
	return remoteRealtimeNodeOperator{client: client}, nil
}

func realtimeGatewayBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "tcp" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%w: invalid gateway_target_url %q", errManagedRealtimeOwnerUnavailable, raw)
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return "", fmt.Errorf("%w: invalid gateway_target_url %q: %w", errManagedRealtimeOwnerUnavailable, raw, err)
	}
	return "http://" + u.Host + "/v1/internal/realtime", nil
}
