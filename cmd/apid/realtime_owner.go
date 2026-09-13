package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

const managedRealtimeOwnerLeaseTTL = 30 * time.Second

var errManagedRealtimeOwnerUnavailable = errors.New("realtime: owner node unavailable")

// realtimeNodeOperator is the node-local management contract plus the
// endpoint/registry operations used by the fleet registrar.
type realtimeNodeOperator interface {
	realtimeOwner
	Connections(context.Context) ([]realtime.ConnectionInfo, error)
	RegisterEndpoint(context.Context, realtime.Endpoint) error
	RemoveEndpoint(context.Context, string) error
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
func (o localRealtimeNodeOperator) Publish(ctx context.Context, endpointID, channel string, message realtime.Message) (int, error) {
	return o.owner.Publish(ctx, endpointID, channel, message)
}
func (o localRealtimeNodeOperator) Connections(ctx context.Context) ([]realtime.ConnectionInfo, error) {
	return o.client.Connections(ctx)
}
func (o localRealtimeNodeOperator) RegisterEndpoint(ctx context.Context, endpoint realtime.Endpoint) error {
	return o.client.RegisterEndpoint(ctx, endpoint)
}
func (o localRealtimeNodeOperator) RemoveEndpoint(ctx context.Context, endpointID string) error {
	return o.client.RemoveEndpoint(ctx, endpointID)
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
func (o remoteRealtimeNodeOperator) Publish(ctx context.Context, endpointID, channel string, message realtime.Message) (int, error) {
	return o.client.Publish(ctx, endpointID, channel, message)
}
func (o remoteRealtimeNodeOperator) Connections(ctx context.Context) ([]realtime.ConnectionInfo, error) {
	return o.client.Connections(ctx)
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
	localNodeID string
	local       realtimeNodeOperator
	log         *slog.Logger
	clientFor   func(state.ComputeNode) (realtimeNodeOperator, error)
	leaseTTL    time.Duration
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

func (o *leasedRealtimeOwner) connectionOperation(ctx context.Context, endpointID, connectionID string, closeAfter bool, fn func(realtimeNodeOperator) error) error {
	for attempt := 0; attempt < 2; attempt++ {
		lease, op, err := o.resolve(ctx, endpointID, connectionID)
		if err != nil {
			return err
		}
		err = fn(op)
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
	return o.connectionOperation(ctx, endpointID, connectionID, false, func(op realtimeNodeOperator) error {
		return op.Send(ctx, endpointID, connectionID, message)
	})
}

func (o *leasedRealtimeOwner) CloseConnection(ctx context.Context, endpointID, connectionID, reason string) error {
	return o.connectionOperation(ctx, endpointID, connectionID, true, func(op realtimeNodeOperator) error {
		return op.CloseConnection(ctx, endpointID, connectionID, reason)
	})
}

func (o *leasedRealtimeOwner) Subscribe(ctx context.Context, endpointID, connectionID, channel string) error {
	return o.connectionOperation(ctx, endpointID, connectionID, false, func(op realtimeNodeOperator) error {
		return op.Subscribe(ctx, endpointID, connectionID, channel)
	})
}

func (o *leasedRealtimeOwner) Unsubscribe(ctx context.Context, endpointID, connectionID, channel string) error {
	return o.connectionOperation(ctx, endpointID, connectionID, false, func(op realtimeNodeOperator) error {
		return op.Unsubscribe(ctx, endpointID, connectionID, channel)
	})
}

// Publish is a fleet broadcast. Channel subscriptions are node-local, so a
// single owner lease cannot represent all recipients. A node that is down is
// tolerated when at least one active node accepted the request; if every node
// is unavailable the caller receives a 503 through the normal owner mapping.
func (o *leasedRealtimeOwner) Publish(ctx context.Context, endpointID, channel string, message realtime.Message) (int, error) {
	if o.nodes == nil {
		return 0, errManagedRealtimeOwnerUnavailable
	}
	nodes, err := o.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return 0, fmt.Errorf("%w: list active nodes: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	queued, reached := 0, 0
	var lastErr error
	for _, node := range nodes {
		op, err := o.nodeOperator(node)
		if err != nil {
			lastErr = err
			continue
		}
		count, err := op.Publish(ctx, endpointID, channel, message)
		if err != nil {
			var managementErr *realtime.ManagementError
			if errors.As(err, &managementErr) && managementErr.StatusCode == http.StatusNotFound {
				// Endpoint registration can lag node activation; continue
				// without turning a healthy partial broadcast into a 503.
				continue
			}
			lastErr = err
			continue
		}
		reached++
		queued += count
	}
	if reached > 0 {
		return queued, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no active realtime nodes accepted publish")
	}
	return 0, fmt.Errorf("%w: %w", errManagedRealtimeOwnerUnavailable, lastErr)
}

// RegisterEndpoint and RemoveEndpoint fan out durable endpoint state to every
// active realtime node. The control-plane row remains authoritative; a node
// restart is repaired by the next endpoint mutation or a future reconciler.
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
