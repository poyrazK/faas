package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	managedRealtimeChannelRouteLimit     = 10000
	managedRealtimeChannelRouteMaxLength = 256
)

// ManagedRealtimeChannelRoute is a conservative routing hint. A stale row can
// cause an extra node publish, but must never cause an active subscriber to be
// skipped.
type ManagedRealtimeChannelRoute struct {
	EndpointID string
	Channel    string
	NodeID     string
}

// ManagedRealtimeChannelRouteStore records which nodes may hold subscribers
// for an endpoint channel. It is optional so narrow state-store integrations
// retain broadcast behavior.
type ManagedRealtimeChannelRouteStore interface {
	AddManagedRealtimeChannelRoutes(context.Context, []ManagedRealtimeChannelRoute) error
	ListManagedRealtimeChannelRouteNodeIDs(context.Context, string, string) ([]string, bool, error)
}

func validateManagedRealtimeChannelRoute(route ManagedRealtimeChannelRoute) error {
	if route.EndpointID == "" || route.NodeID == "" || route.Channel == "" ||
		len(route.Channel) > managedRealtimeChannelRouteMaxLength || strings.TrimSpace(route.Channel) != route.Channel ||
		strings.ContainsAny(route.Channel, "/?#\r\n") {
		return errors.New("state: endpoint, node, and valid channel are required for realtime channel route")
	}
	return nil
}

func (s *PgStore) AddManagedRealtimeChannelRoutes(ctx context.Context, routes []ManagedRealtimeChannelRoute) error {
	if len(routes) == 0 {
		return nil
	}
	endpointIDs := make([]string, 0, len(routes))
	nodeIDs := make([]string, 0, len(routes))
	channels := make([]string, 0, len(routes))
	seen := make(map[ManagedRealtimeChannelRoute]struct{}, len(routes))
	for _, route := range routes {
		if err := validateManagedRealtimeChannelRoute(route); err != nil {
			return err
		}
		if _, ok := seen[route]; ok {
			continue
		}
		seen[route] = struct{}{}
		endpointIDs = append(endpointIDs, route.EndpointID)
		nodeIDs = append(nodeIDs, route.NodeID)
		channels = append(channels, route.Channel)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin realtime channel route update: %w", err)
	}
	defer tx.Rollback(ctx)
	endpointSet := make(map[string]struct{}, len(endpointIDs))
	for _, endpointID := range endpointIDs {
		endpointSet[endpointID] = struct{}{}
	}
	lockEndpointIDs := make([]string, 0, len(endpointSet))
	for endpointID := range endpointSet {
		lockEndpointIDs = append(lockEndpointIDs, endpointID)
	}
	sort.Strings(lockEndpointIDs)
	lockedEndpoints, err := tx.Query(ctx, `
		select id from managed_realtime_endpoints
		 where id = any($1::uuid[])
	 order by id
	 for update`, lockEndpointIDs)
	if err != nil {
		return fmt.Errorf("state: lock realtime channel route endpoints: %w", err)
	}
	for lockedEndpoints.Next() {
		var endpointID string
		if err := lockedEndpoints.Scan(&endpointID); err != nil {
			lockedEndpoints.Close()
			return fmt.Errorf("state: scan realtime channel route endpoint: %w", err)
		}
	}
	if err := lockedEndpoints.Err(); err != nil {
		lockedEndpoints.Close()
		return fmt.Errorf("state: iterate realtime channel route endpoints: %w", err)
	}
	lockedEndpoints.Close()
	_, err = tx.Exec(ctx, `
		insert into managed_realtime_channel_routes (endpoint_id, node_id, channel)
		select endpoint_id::uuid, node_id::uuid, channel
		  from unnest($1::text[], $2::text[], $3::text[]) as incoming(endpoint_id, node_id, channel)
		 where not exists (
		   select 1 from managed_realtime_channel_route_overflow overflow
		    where overflow.endpoint_id = incoming.endpoint_id::uuid
		 )
		on conflict (endpoint_id, node_id, channel) do nothing`, endpointIDs, nodeIDs, channels)
	if err != nil {
		return fmt.Errorf("state: add realtime channel routes: %w", err)
	}
	_, err = tx.Exec(ctx, `
		insert into managed_realtime_channel_route_overflow (endpoint_id)
		select endpoint_id
		  from managed_realtime_channel_routes
		 where endpoint_id = any($2::uuid[])
		 group by endpoint_id
		having count(*) > $1
		on conflict (endpoint_id) do nothing`, managedRealtimeChannelRouteLimit, lockEndpointIDs)
	if err != nil {
		return fmt.Errorf("state: disable oversized realtime channel route index: %w", err)
	}
	_, err = tx.Exec(ctx, `
		delete from managed_realtime_channel_routes routes
		 using managed_realtime_channel_route_overflow overflow
		 where routes.endpoint_id = overflow.endpoint_id
		   and routes.endpoint_id = any($1::uuid[])`, lockEndpointIDs)
	if err != nil {
		return fmt.Errorf("state: clear oversized realtime channel route index: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit realtime channel route update: %w", err)
	}
	return nil
}

func (s *PgStore) ListManagedRealtimeChannelRouteNodeIDs(ctx context.Context, endpointID, channel string) ([]string, bool, error) {
	if endpointID == "" || channel == "" {
		return nil, false, nil
	}
	var disabled bool
	var nodeIDs []string
	// Read the overflow marker and rows in one statement snapshot. Overflow
	// inserts its marker and clears routes in one transaction; separate reads
	// could otherwise observe the marker before the clear and then miss every
	// route during the transition.
	if err := s.pool.QueryRow(ctx, `
		select exists (
		         select 1 from managed_realtime_channel_route_overflow where endpoint_id = $1
	       ),
	       coalesce(array_agg(distinct routes.node_id::text order by routes.node_id::text), '{}'::text[])
	  from managed_realtime_channel_routes routes
	 where routes.endpoint_id = $1 and routes.channel = $2`, endpointID, channel).Scan(&disabled, &nodeIDs); err != nil {
		return nil, false, fmt.Errorf("state: list realtime channel route nodes: %w", err)
	}
	return nodeIDs, disabled, nil
}

func (m *MemStore) AddManagedRealtimeChannelRoutes(_ context.Context, routes []ManagedRealtimeChannelRoute) error {
	for _, route := range routes {
		if err := validateManagedRealtimeChannelRoute(route); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, route := range routes {
		if m.realtimeChannelRouteOverflow[route.EndpointID] {
			continue
		}
		if _, exists := m.realtimeChannelRoutes[route]; exists {
			continue
		}
		if m.realtimeChannelRouteCounts[route.EndpointID] >= managedRealtimeChannelRouteLimit {
			m.realtimeChannelRouteOverflow[route.EndpointID] = true
			for existing := range m.realtimeChannelRoutes {
				if existing.EndpointID == route.EndpointID {
					delete(m.realtimeChannelRoutes, existing)
				}
			}
			delete(m.realtimeChannelRouteCounts, route.EndpointID)
			continue
		}
		m.realtimeChannelRoutes[route] = struct{}{}
		m.realtimeChannelRouteCounts[route.EndpointID]++
	}
	return nil
}

func (m *MemStore) ListManagedRealtimeChannelRouteNodeIDs(_ context.Context, endpointID, channel string) ([]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.realtimeChannelRouteOverflow[endpointID] {
		return nil, true, nil
	}
	nodes := make(map[string]struct{})
	for route := range m.realtimeChannelRoutes {
		if route.EndpointID == endpointID && route.Channel == channel {
			nodes[route.NodeID] = struct{}{}
		}
	}
	result := make([]string, 0, len(nodes))
	for nodeID := range nodes {
		result = append(result, nodeID)
	}
	sort.Strings(result)
	return result, false, nil
}
