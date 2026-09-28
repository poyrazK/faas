package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	managedRealtimeChannelRouteLimit        = 10000
	managedRealtimeChannelRouteMaxLength    = 256
	managedRealtimeChannelRouteRebuildDelay = 5 * time.Minute
	managedRealtimeChannelRouteLockLease    = 10 * time.Minute
)

// ManagedRealtimeChannelRoute is a node-level hint. A stale row can cause an
// extra node publish, but must never cause an active subscriber to be skipped.
type ManagedRealtimeChannelRoute struct {
	EndpointID string
	Channel    string
	NodeID     string
}

// ManagedRealtimeChannelRouteView is one consistent directory snapshot.
// ReadyNodeIDs have a complete snapshot for the current generation; all other
// active nodes must remain in the publish fanout. Disabled means this
// endpoint-channel pair must use the full-fleet fallback.
type ManagedRealtimeChannelRouteView struct {
	NodeIDs      []string
	ReadyNodeIDs []string
	Disabled     bool
}

// ManagedRealtimeChannelPublishTarget is the small compute-node projection
// needed to dispatch one channel publish. SnapshotReady is false for nodes
// that must stay in the conservative fanout.
type ManagedRealtimeChannelPublishTarget struct {
	NodeID           string
	NodeName         string
	GatewayTargetURL *string
	SnapshotReady    bool
}

// ManagedRealtimeChannelPublishTargetView is produced from one directory
// snapshot and contains only nodes that may receive the channel publish.
// HasActiveNodes distinguishes an empty fleet from a ready directory with no
// subscribers. Disabled means the caller must retain full-fleet fanout.
type ManagedRealtimeChannelPublishTargetView struct {
	Targets        []ManagedRealtimeChannelPublishTarget
	HasActiveNodes bool
	Disabled       bool
}

// ManagedRealtimeChannelRouteLock serializes a node's Subscribe operation
// with its live-connection snapshot and route replacement.
type ManagedRealtimeChannelRouteLock interface {
	Release(context.Context)
}

type managedRealtimeChannelRouteLockFunc func(context.Context)

func (f managedRealtimeChannelRouteLockFunc) Release(ctx context.Context) { f(ctx) }

// ManagedRealtimeChannelRouteStore is optional so narrow state-store
// integrations retain full-broadcast behavior.
type ManagedRealtimeChannelRouteStore interface {
	AcquireManagedRealtimeChannelRouteLock(context.Context, string) (ManagedRealtimeChannelRouteLock, error)
	AddManagedRealtimeChannelRoutes(context.Context, []ManagedRealtimeChannelRoute) error
	CurrentManagedRealtimeChannelRouteGeneration(context.Context) (int64, error)
	ReplaceManagedRealtimeChannelRoutes(context.Context, string, int64, []ManagedRealtimeChannelRoute) error
	BeginManagedRealtimeChannelRouteRebuild(context.Context) (bool, error)
	ListManagedRealtimeChannelRouteView(context.Context, string, string) (ManagedRealtimeChannelRouteView, error)
	FinalizeManagedRealtimeChannelRouteRebuild(context.Context) (bool, error)
}

// ManagedRealtimeChannelPublishTargetStore lets the publish hot path fetch
// only routed nodes and nodes whose snapshots are not current. Implementors
// that do not provide this optimization continue to use the route-view path.
type ManagedRealtimeChannelPublishTargetStore interface {
	ListManagedRealtimeChannelPublishTargets(context.Context, string, string) (ManagedRealtimeChannelPublishTargetView, error)
}

var (
	_ ManagedRealtimeChannelRouteStore         = (*PgStore)(nil)
	_ ManagedRealtimeChannelRouteStore         = (*MemStore)(nil)
	_ ManagedRealtimeChannelPublishTargetStore = (*PgStore)(nil)
	_ ManagedRealtimeChannelPublishTargetStore = (*MemStore)(nil)
)

type managedRealtimeChannelRouteOverflowState struct {
	RebuildGeneration int64
	Rebuilding        bool
	OverflowAll       bool
	OverflowChannels  map[string]struct{}
	NextRebuildAt     time.Time
	RebuildStartedAt  time.Time
}

func validateManagedRealtimeChannelRoute(route ManagedRealtimeChannelRoute) error {
	if route.EndpointID == "" || route.NodeID == "" || route.Channel == "" ||
		len(route.Channel) > managedRealtimeChannelRouteMaxLength || strings.TrimSpace(route.Channel) != route.Channel ||
		strings.ContainsAny(route.Channel, "/?#\r\n") {
		return errors.New("state: endpoint, node, and valid channel are required for realtime channel route")
	}
	return nil
}

func uniqueManagedRealtimeChannelRoutes(routes []ManagedRealtimeChannelRoute) ([]ManagedRealtimeChannelRoute, error) {
	unique := make([]ManagedRealtimeChannelRoute, 0, len(routes))
	seen := make(map[ManagedRealtimeChannelRoute]struct{}, len(routes))
	for _, route := range routes {
		if err := validateManagedRealtimeChannelRoute(route); err != nil {
			return nil, err
		}
		if _, ok := seen[route]; ok {
			continue
		}
		seen[route] = struct{}{}
		unique = append(unique, route)
	}
	return unique, nil
}

func (s *PgStore) AcquireManagedRealtimeChannelRouteLock(ctx context.Context, nodeID string) (ManagedRealtimeChannelRouteLock, error) {
	if nodeID == "" {
		return nil, errors.New("state: node id is required for realtime channel route lock")
	}
	release, err := s.acquireSessionAdvisoryLock(ctx, sessionAdvisoryLock{
		what:      "managed realtime channel route snapshot",
		tryLock:   `select pg_try_advisory_lock(hashtextextended('managed-realtime-channel-route:' || $1, 0))`,
		unlock:    `select pg_advisory_unlock(hashtextextended('managed-realtime-channel-route:' || $1, 0))`,
		keyArg:    nodeID,
		retryWait: 50 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	return managedRealtimeChannelRouteLockFunc(func(ctx context.Context) { release(ctx) }), nil
}

func (s *PgStore) AddManagedRealtimeChannelRoutes(ctx context.Context, routes []ManagedRealtimeChannelRoute) error {
	unique, err := uniqueManagedRealtimeChannelRoutes(routes)
	if err != nil || len(unique) == 0 {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin realtime channel route update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	endpointIDs := managedRealtimeChannelRouteEndpointIDs(unique)
	if err := lockManagedRealtimeChannelRouteEndpoints(ctx, tx, endpointIDs); err != nil {
		return err
	}
	if err := insertManagedRealtimeChannelRoutes(ctx, tx, unique); err != nil {
		return err
	}
	if err := enforceManagedRealtimeChannelRouteLimit(ctx, tx, endpointIDs); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit realtime channel route update: %w", err)
	}
	return nil
}

func (s *PgStore) CurrentManagedRealtimeChannelRouteGeneration(ctx context.Context) (int64, error) {
	var generation int64
	if err := s.pool.QueryRow(ctx, `
		select generation from managed_realtime_channel_route_generation where singleton = true
	`).Scan(&generation); err != nil {
		return 0, fmt.Errorf("state: read realtime channel route generation: %w", err)
	}
	return generation, nil
}

func (s *PgStore) ReplaceManagedRealtimeChannelRoutes(ctx context.Context, nodeID string, snapshotGeneration int64, routes []ManagedRealtimeChannelRoute) error {
	if nodeID == "" || snapshotGeneration < 0 {
		return errors.New("state: node id and non-negative route snapshot generation are required")
	}
	unique, err := uniqueManagedRealtimeChannelRoutes(routes)
	if err != nil {
		return err
	}
	for _, route := range unique {
		if route.NodeID != nodeID {
			return errors.New("state: route snapshot contains a different node id")
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin realtime channel route snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	endpointIDs := managedRealtimeChannelRouteEndpointIDs(unique)
	oldRows, err := tx.Query(ctx, `
		select distinct endpoint_id::text
		  from managed_realtime_channel_routes
		 where node_id = $1
	`, nodeID)
	if err != nil {
		return fmt.Errorf("state: list previous node channel routes: %w", err)
	}
	for oldRows.Next() {
		var endpointID string
		if err := oldRows.Scan(&endpointID); err != nil {
			oldRows.Close()
			return fmt.Errorf("state: scan previous node channel route: %w", err)
		}
		endpointIDs = append(endpointIDs, endpointID)
	}
	if err := oldRows.Err(); err != nil {
		oldRows.Close()
		return fmt.Errorf("state: iterate previous node channel routes: %w", err)
	}
	oldRows.Close()
	endpointIDs = uniqueStrings(endpointIDs)
	if err := lockManagedRealtimeChannelRouteEndpoints(ctx, tx, endpointIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from managed_realtime_channel_routes where node_id = $1`, nodeID); err != nil {
		return fmt.Errorf("state: replace node realtime channel routes: %w", err)
	}
	if err := insertManagedRealtimeChannelRoutes(ctx, tx, unique); err != nil {
		return err
	}
	if err := enforceManagedRealtimeChannelRouteLimit(ctx, tx, endpointIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into managed_realtime_channel_route_node_state (node_id, snapshot_generation)
		values ($1, $2)
		on conflict (node_id) do update set
			snapshot_generation = excluded.snapshot_generation,
			updated_at = now()
	`, nodeID, snapshotGeneration); err != nil {
		return fmt.Errorf("state: record realtime channel route snapshot generation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update managed_realtime_channel_route_overflow
		   set rebuild_started_at = now()
		 where rebuilding = true and rebuild_generation <= $1
	`, snapshotGeneration); err != nil {
		return fmt.Errorf("state: renew realtime channel route rebuild lease: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit realtime channel route snapshot: %w", err)
	}
	return nil
}

func (s *PgStore) BeginManagedRealtimeChannelRouteRebuild(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("state: begin realtime channel route rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var generation int64
	if err := tx.QueryRow(ctx, `
		select generation from managed_realtime_channel_route_generation
		 where singleton = true for update
	`).Scan(&generation); err != nil {
		return false, fmt.Errorf("state: lock realtime channel route generation: %w", err)
	}
	rows, err := tx.Query(ctx, `
		select due.endpoint_id::text
		  from (
		    select endpoint_id
		      from managed_realtime_channel_route_overflow
		     where (not rebuilding and next_rebuild_at <= now())
		        or (rebuilding and (rebuild_started_at is null or rebuild_started_at < now() - ($1::double precision * interval '1 second')))
		    union
		    select endpoint_id
		      from managed_realtime_channel_route_overflow_channels
		     where next_rebuild_at <= now()
		       and not exists (
		         select 1 from managed_realtime_channel_route_overflow overflow
		          where overflow.endpoint_id = managed_realtime_channel_route_overflow_channels.endpoint_id
		            and overflow.rebuilding = true
		       )
		  ) due
		 order by due.endpoint_id`, int64(managedRealtimeChannelRouteLockLease.Seconds()))
	if err != nil {
		return false, fmt.Errorf("state: list realtime channel routes to rebuild: %w", err)
	}
	endpointIDs := make([]string, 0)
	for rows.Next() {
		var endpointID string
		if err := rows.Scan(&endpointID); err != nil {
			rows.Close()
			return false, fmt.Errorf("state: scan realtime channel route rebuild endpoint: %w", err)
		}
		endpointIDs = append(endpointIDs, endpointID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("state: iterate realtime channel route rebuild endpoints: %w", err)
	}
	rows.Close()
	if len(endpointIDs) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("state: finish realtime channel route rebuild check: %w", err)
		}
		return false, nil
	}
	if err := tx.QueryRow(ctx, `
		update managed_realtime_channel_route_generation
		   set generation = generation + 1
		 where singleton = true
		 returning generation
	`).Scan(&generation); err != nil {
		return false, fmt.Errorf("state: advance realtime channel route generation: %w", err)
	}
	// Older apid versions treat any endpoint row here as full overflow. Keep
	// that marker only for the rebuild window; selective state lives in the
	// channel table so mixed-version route snapshots stay safe.
	if _, err := tx.Exec(ctx, `
		insert into managed_realtime_channel_route_overflow
			(endpoint_id, rebuild_generation, rebuilding, next_rebuild_at, rebuild_started_at, overflow_all)
		select endpoint_id::uuid, $1, true,
		       now() + ($2::double precision * interval '1 second'), now(), true
		  from unnest($3::text[]) as endpoint_id
		on conflict (endpoint_id) do update set
			rebuild_generation = excluded.rebuild_generation,
			rebuilding = true,
			next_rebuild_at = excluded.next_rebuild_at,
			rebuild_started_at = excluded.rebuild_started_at,
			overflow_all = true
	`, generation, int64(managedRealtimeChannelRouteLockLease.Seconds()), endpointIDs); err != nil {
		return false, fmt.Errorf("state: mark realtime channel route rebuild: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		delete from managed_realtime_channel_route_overflow_channels
		 where endpoint_id = any($1::uuid[])
	`, endpointIDs); err != nil {
		return false, fmt.Errorf("state: reset realtime channel route overflow channels for rebuild: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: commit realtime channel route rebuild: %w", err)
	}
	return true, nil
}

func (s *PgStore) ListManagedRealtimeChannelRouteView(ctx context.Context, endpointID, channel string) (ManagedRealtimeChannelRouteView, error) {
	var view ManagedRealtimeChannelRouteView
	if endpointID == "" || channel == "" {
		return view, nil
	}
	// All results come from one MVCC snapshot: overflow status, recipient
	// rows, and node readiness cannot describe different rebuild phases.
	if err := s.pool.QueryRow(ctx, `
		select exists (
		         select 1 from managed_realtime_channel_route_overflow overflow
	          where overflow.endpoint_id = $1
	       ) or exists (
	         select 1
	           from managed_realtime_channel_route_overflow_channels channels
	          where channels.endpoint_id = $1 and channels.channel = $2
	       ),
	       coalesce((
	         select array_agg(distinct routes.node_id::text order by routes.node_id::text)
	           from managed_realtime_channel_routes routes
	          where routes.endpoint_id = $1 and routes.channel = $2
	       ), '{}'::text[]),
	       coalesce((
	         select array_agg(node_state.node_id::text order by node_state.node_id::text)
	           from managed_realtime_channel_route_node_state node_state
	           join managed_realtime_channel_route_generation generation on generation.singleton = true
	          where node_state.snapshot_generation >= generation.generation
	       ), '{}'::text[])
	`, endpointID, channel).Scan(&view.Disabled, &view.NodeIDs, &view.ReadyNodeIDs); err != nil {
		return ManagedRealtimeChannelRouteView{}, fmt.Errorf("state: list realtime channel route view: %w", err)
	}
	return view, nil
}

func (s *PgStore) ListManagedRealtimeChannelPublishTargets(ctx context.Context, endpointID, channel string) (ManagedRealtimeChannelPublishTargetView, error) {
	view := ManagedRealtimeChannelPublishTargetView{Targets: make([]ManagedRealtimeChannelPublishTarget, 0)}
	if endpointID == "" || channel == "" {
		nodes, err := s.ActiveComputeNodes(ctx)
		if err != nil {
			return ManagedRealtimeChannelPublishTargetView{}, err
		}
		view.HasActiveNodes = len(nodes) > 0
		for _, node := range nodes {
			view.Targets = append(view.Targets, ManagedRealtimeChannelPublishTarget{
				NodeID: node.ID, NodeName: node.Name, GatewayTargetURL: node.GatewayTargetURL,
			})
		}
		return view, nil
	}
	rows, err := s.pool.Query(ctx, `
		with route_state as (
			select exists (
			         select 1 from managed_realtime_channel_route_overflow overflow
			          where overflow.endpoint_id = $1
			       ) or exists (
			         select 1 from managed_realtime_channel_route_overflow_channels overflow_channel
			          where overflow_channel.endpoint_id = $1 and overflow_channel.channel = $2
			       ) as disabled,
			       coalesce((
			         select generation from managed_realtime_channel_route_generation
			          where singleton = true
			       ), 0) as generation,
			       exists (select 1 from compute_nodes where active = true) as has_active_nodes
		), candidates as (
			select nodes.id::text as node_id,
			       nodes.name as node_name,
			       nodes.gateway_target_url,
			       ($1 <> '' and $2 <> '' and
			        coalesce(node_state.snapshot_generation >= route_state.generation, false)) as snapshot_ready
			  from compute_nodes nodes
			 cross join route_state
			  left join managed_realtime_channel_route_node_state node_state
			    on node_state.node_id = nodes.id
			 where nodes.active = true
		   and (
			      route_state.disabled
			      or exists (
			           select 1 from managed_realtime_channel_routes routes
			            where routes.endpoint_id = $1 and routes.channel = $2 and routes.node_id = nodes.id
			      )
			      or not ($1 <> '' and $2 <> '' and
			              coalesce(node_state.snapshot_generation >= route_state.generation, false))
		   )
		)
		select route_state.disabled,
		       route_state.has_active_nodes,
		       candidates.node_id,
		       candidates.node_name,
		       candidates.gateway_target_url,
		       candidates.snapshot_ready
		  from route_state
		  left join candidates on true
		 order by candidates.node_name
	`, endpointID, channel)
	if err != nil {
		return ManagedRealtimeChannelPublishTargetView{}, fmt.Errorf("state: list realtime channel publish targets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var disabled, hasActiveNodes bool
		var nodeID, nodeName, gatewayTargetURL pgtype.Text
		var snapshotReady pgtype.Bool
		if err := rows.Scan(&disabled, &hasActiveNodes, &nodeID, &nodeName, &gatewayTargetURL, &snapshotReady); err != nil {
			return ManagedRealtimeChannelPublishTargetView{}, fmt.Errorf("state: scan realtime channel publish target: %w", err)
		}
		view.Disabled = disabled
		view.HasActiveNodes = hasActiveNodes
		if !nodeID.Valid {
			continue
		}
		target := ManagedRealtimeChannelPublishTarget{
			NodeID:        nodeID.String,
			NodeName:      nodeName.String,
			SnapshotReady: snapshotReady.Valid && snapshotReady.Bool,
		}
		if gatewayTargetURL.Valid {
			url := gatewayTargetURL.String
			target.GatewayTargetURL = &url
		}
		view.Targets = append(view.Targets, target)
	}
	if err := rows.Err(); err != nil {
		return ManagedRealtimeChannelPublishTargetView{}, fmt.Errorf("state: list realtime channel publish targets: %w", err)
	}
	return view, nil
}

func (s *PgStore) FinalizeManagedRealtimeChannelRouteRebuild(ctx context.Context) (bool, error) {
	if err := s.pruneInactiveManagedRealtimeChannelRoutes(ctx); err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("state: begin realtime channel route rebuild finalization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		select endpoint_id::text
		  from managed_realtime_channel_route_overflow
		 where rebuilding = true
	 order by endpoint_id
	`)
	if err != nil {
		return false, fmt.Errorf("state: list realtime channel route rebuild finalization endpoints: %w", err)
	}
	endpointIDs := make([]string, 0)
	for rows.Next() {
		var endpointID string
		if err := rows.Scan(&endpointID); err != nil {
			rows.Close()
			return false, fmt.Errorf("state: scan realtime channel route finalization endpoint: %w", err)
		}
		endpointIDs = append(endpointIDs, endpointID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("state: iterate realtime channel route finalization endpoints: %w", err)
	}
	rows.Close()
	if err := lockManagedRealtimeChannelRouteEndpoints(ctx, tx, endpointIDs); err != nil {
		return false, err
	}
	// Match the endpoint -> generation lock order used by Subscribe. Keeping
	// that order prevents an over-cap insert from deadlocking with finalization.
	var generation int64
	if err := tx.QueryRow(ctx, `
		select generation from managed_realtime_channel_route_generation
		 where singleton = true for update
	`).Scan(&generation); err != nil {
		return false, fmt.Errorf("state: lock realtime channel route generation for finalization: %w", err)
	}
	var unsynced bool
	if err := tx.QueryRow(ctx, `
		select exists (
		  select 1 from compute_nodes nodes
		  left join managed_realtime_channel_route_node_state node_state on node_state.node_id = nodes.id
		   where nodes.active = true
		     and coalesce(node_state.snapshot_generation, -1) < $1
		)
	`, generation).Scan(&unsynced); err != nil {
		return false, fmt.Errorf("state: check realtime channel route snapshot coverage: %w", err)
	}
	if unsynced {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("state: finish incomplete realtime channel route rebuild: %w", err)
		}
		return false, nil
	}
	rows, err = tx.Query(ctx, `
		select endpoint_id::text
		  from managed_realtime_channel_route_overflow
		 where rebuilding = true and endpoint_id = any($1::uuid[])
	 order by endpoint_id
	 for update`, endpointIDs)
	if err != nil {
		return false, fmt.Errorf("state: lock realtime channel route rebuild finalization rows: %w", err)
	}
	endpointIDs = endpointIDs[:0]
	for rows.Next() {
		var endpointID string
		if err := rows.Scan(&endpointID); err != nil {
			rows.Close()
			return false, fmt.Errorf("state: scan realtime channel route finalization row: %w", err)
		}
		endpointIDs = append(endpointIDs, endpointID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("state: iterate realtime channel route finalization rows: %w", err)
	}
	rows.Close()
	if err := enforceManagedRealtimeChannelRouteLimit(ctx, tx, endpointIDs); err != nil {
		return false, err
	}
	for _, endpointID := range endpointIDs {
		var count int
		if err := tx.QueryRow(ctx, `
			select count(*) from managed_realtime_channel_routes where endpoint_id = $1
		`, endpointID).Scan(&count); err != nil {
			return false, fmt.Errorf("state: count rebuilt realtime channel routes: %w", err)
		}
		var overflowChannels int
		if err := tx.QueryRow(ctx, `
			select count(*) from managed_realtime_channel_route_overflow_channels where endpoint_id = $1
		`, endpointID).Scan(&overflowChannels); err != nil {
			return false, fmt.Errorf("state: count rebuilt realtime route overflow channels: %w", err)
		}
		if count <= managedRealtimeChannelRouteLimit && overflowChannels == 0 {
			if _, err := tx.Exec(ctx, `delete from managed_realtime_channel_route_overflow where endpoint_id = $1`, endpointID); err != nil {
				return false, fmt.Errorf("state: re-enable realtime channel route index: %w", err)
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			update managed_realtime_channel_route_overflow_channels
			   set next_rebuild_at = now() + interval '5 minutes'
			 where endpoint_id = $1
		`, endpointID); err != nil {
			return false, fmt.Errorf("state: schedule channel-scoped realtime route rebuild: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from managed_realtime_channel_route_overflow where endpoint_id = $1`, endpointID); err != nil {
			return false, fmt.Errorf("state: clear endpoint-wide realtime route rebuild state: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: commit realtime channel route rebuild finalization: %w", err)
	}
	return true, nil
}

func (s *PgStore) pruneInactiveManagedRealtimeChannelRoutes(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `
		select nodes.id::text
		  from compute_nodes nodes
		 where nodes.active = false
	   and (
	     exists (select 1 from managed_realtime_channel_routes routes where routes.node_id = nodes.id)
	     or exists (select 1 from managed_realtime_channel_route_node_state node_state where node_state.node_id = nodes.id)
	   )
	 order by nodes.id
	`)
	if err != nil {
		return fmt.Errorf("state: list inactive realtime channel route nodes: %w", err)
	}
	var nodeIDs []string
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			rows.Close()
			return fmt.Errorf("state: scan inactive realtime channel route node: %w", err)
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("state: iterate inactive realtime channel route nodes: %w", err)
	}
	rows.Close()
	for _, nodeID := range nodeIDs {
		lock, err := s.AcquireManagedRealtimeChannelRouteLock(ctx, nodeID)
		if err != nil {
			return err
		}
		err = func() error {
			defer lock.Release(ctx)
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				return fmt.Errorf("state: begin inactive realtime route cleanup: %w", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			var active bool
			if err := tx.QueryRow(ctx, `select active from compute_nodes where id = $1 for update`, nodeID).Scan(&active); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("state: check inactive realtime route node: %w", err)
			}
			if !active {
				if _, err := tx.Exec(ctx, `delete from managed_realtime_channel_routes where node_id = $1`, nodeID); err != nil {
					return fmt.Errorf("state: prune inactive realtime channel routes: %w", err)
				}
				if _, err := tx.Exec(ctx, `delete from managed_realtime_channel_route_node_state where node_id = $1`, nodeID); err != nil {
					return fmt.Errorf("state: invalidate inactive realtime route snapshot: %w", err)
				}
			}
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("state: commit inactive realtime route cleanup: %w", err)
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func managedRealtimeChannelRouteEndpointIDs(routes []ManagedRealtimeChannelRoute) []string {
	ids := make([]string, 0, len(routes))
	for _, route := range routes {
		ids = append(ids, route.EndpointID)
	}
	return uniqueStrings(ids)
}

func uniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	unique := make([]string, 0, len(set))
	for value := range set {
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func lockManagedRealtimeChannelRouteEndpoints(ctx context.Context, tx pgx.Tx, endpointIDs []string) error {
	if len(endpointIDs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		select id from managed_realtime_endpoints
		 where id = any($1::uuid[])
	 order by id
	 for update`, endpointIDs)
	if err != nil {
		return fmt.Errorf("state: lock realtime channel route endpoints: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var endpointID string
		if err := rows.Scan(&endpointID); err != nil {
			return fmt.Errorf("state: scan realtime channel route endpoint: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("state: iterate realtime channel route endpoints: %w", err)
	}
	return nil
}

func insertManagedRealtimeChannelRoutes(ctx context.Context, tx pgx.Tx, routes []ManagedRealtimeChannelRoute) error {
	if len(routes) == 0 {
		return nil
	}
	endpointIDs := make([]string, 0, len(routes))
	nodeIDs := make([]string, 0, len(routes))
	channels := make([]string, 0, len(routes))
	for _, route := range routes {
		endpointIDs = append(endpointIDs, route.EndpointID)
		nodeIDs = append(nodeIDs, route.NodeID)
		channels = append(channels, route.Channel)
	}
	_, err := tx.Exec(ctx, `
		insert into managed_realtime_channel_routes (endpoint_id, channel, node_id)
		select incoming.endpoint_id::uuid, incoming.channel, incoming.node_id::uuid
		  from unnest($1::text[], $2::text[], $3::text[]) as incoming(endpoint_id, node_id, channel)
		 where not exists (
		   select 1 from managed_realtime_channel_route_overflow_channels overflow_channel
		    where overflow_channel.endpoint_id = incoming.endpoint_id::uuid
		      and overflow_channel.channel = incoming.channel
		 )
		   and not exists (
		     select 1 from managed_realtime_channel_route_overflow overflow
		      where overflow.endpoint_id = incoming.endpoint_id::uuid
		        and overflow.rebuilding = false
		   )
		on conflict (endpoint_id, channel, node_id) do nothing`, endpointIDs, nodeIDs, channels)
	if err != nil {
		return fmt.Errorf("state: add realtime channel routes: %w", err)
	}
	return nil
}

func enforceManagedRealtimeChannelRouteLimit(ctx context.Context, tx pgx.Tx, endpointIDs []string) error {
	if len(endpointIDs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		select endpoint_id::text
		  from managed_realtime_channel_routes
		 where endpoint_id = any($1::uuid[])
		 group by endpoint_id
		having count(*) > $2
		 order by endpoint_id`, endpointIDs, managedRealtimeChannelRouteLimit)
	if err != nil {
		return fmt.Errorf("state: inspect realtime channel route limit: %w", err)
	}
	var overLimitEndpoints []string
	for rows.Next() {
		var endpointID string
		if err := rows.Scan(&endpointID); err != nil {
			rows.Close()
			return fmt.Errorf("state: scan over-limit realtime channel route: %w", err)
		}
		overLimitEndpoints = append(overLimitEndpoints, endpointID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("state: iterate over-limit realtime channel routes: %w", err)
	}
	rows.Close()
	if len(overLimitEndpoints) == 0 {
		return nil
	}
	for _, endpointID := range overLimitEndpoints {
		var routeCount int
		if err := tx.QueryRow(ctx, `
			select count(*) from managed_realtime_channel_routes where endpoint_id = $1
		`, endpointID).Scan(&routeCount); err != nil {
			return fmt.Errorf("state: count over-limit realtime channel routes: %w", err)
		}
		var rebuilding bool
		err := tx.QueryRow(ctx, `
			select rebuilding
			  from managed_realtime_channel_route_overflow
			 where endpoint_id = $1
		`, endpointID).Scan(&rebuilding)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("state: read realtime channel route overflow state: %w", err)
		}
		if err == nil && !rebuilding {
			if _, err := tx.Exec(ctx, `delete from managed_realtime_channel_routes where endpoint_id = $1`, endpointID); err != nil {
				return fmt.Errorf("state: clear legacy over-cap realtime channel routes: %w", err)
			}
			continue
		}

		type channelCount struct {
			channel string
			count   int
		}
		channelRows, err := tx.Query(ctx, `
			select channel, count(*)
			  from managed_realtime_channel_routes
			 where endpoint_id = $1
			 group by channel
			 order by count(*) desc, channel
		`, endpointID)
		if err != nil {
			return fmt.Errorf("state: group over-limit realtime routes by channel: %w", err)
		}
		overflowChannels := make([]string, 0)
		toRemove := routeCount - managedRealtimeChannelRouteLimit
		for channelRows.Next() {
			if toRemove <= 0 {
				continue
			}
			var item channelCount
			if err := channelRows.Scan(&item.channel, &item.count); err != nil {
				channelRows.Close()
				return fmt.Errorf("state: scan over-limit realtime route channel: %w", err)
			}
			overflowChannels = append(overflowChannels, item.channel)
			toRemove -= item.count
		}
		if err := channelRows.Err(); err != nil {
			channelRows.Close()
			return fmt.Errorf("state: iterate over-limit realtime route channels: %w", err)
		}
		channelRows.Close()
		if toRemove > 0 {
			return fmt.Errorf("state: could not compact over-limit realtime channel routes for endpoint %s", endpointID)
		}
		// Older apid versions treat any row in the endpoint overflow table as
		// endpoint-wide fallback, so persist selective markers separately.
		if _, err := tx.Exec(ctx, `
			insert into managed_realtime_channel_route_overflow_channels (endpoint_id, channel)
			select $1::uuid, channel
			  from unnest($2::text[]) as selected(channel)
			on conflict (endpoint_id, channel) do nothing
		`, endpointID, overflowChannels); err != nil {
			return fmt.Errorf("state: mark overflowing realtime channels: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			delete from managed_realtime_channel_routes
			 where endpoint_id = $1 and channel = any($2::text[])
		`, endpointID, overflowChannels); err != nil {
			return fmt.Errorf("state: clear overflowing realtime channel routes: %w", err)
		}
	}
	return nil
}

// MemStore implementations mirror the persistent routing contract.
func (m *MemStore) AcquireManagedRealtimeChannelRouteLock(ctx context.Context, nodeID string) (ManagedRealtimeChannelRouteLock, error) {
	if nodeID == "" {
		return nil, errors.New("state: node id is required for realtime channel route lock")
	}
	m.mu.Lock()
	if m.realtimeChannelRouteLocks == nil {
		m.realtimeChannelRouteLocks = make(map[string]chan struct{})
	}
	lock := m.realtimeChannelRouteLocks[nodeID]
	if lock == nil {
		lock = make(chan struct{}, 1)
		m.realtimeChannelRouteLocks[nodeID] = lock
	}
	m.mu.Unlock()
	select {
	case lock <- struct{}{}:
		return managedRealtimeChannelRouteLockFunc(func(context.Context) { <-lock }), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *MemStore) AddManagedRealtimeChannelRoutes(_ context.Context, routes []ManagedRealtimeChannelRoute) error {
	unique, err := uniqueManagedRealtimeChannelRoutes(routes)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureRealtimeChannelRouteMapsLocked()
	for _, route := range unique {
		overflow, disabled := m.realtimeChannelRouteOverflow[route.EndpointID]
		if disabled && ((overflow.OverflowAll && !overflow.Rebuilding) || managedRealtimeChannelRouteChannelOverflowed(overflow, route.Channel)) {
			continue
		}
		m.addRealtimeChannelRouteLocked(route)
	}
	return m.enforceMemRealtimeChannelRouteLimitLocked(managedRealtimeChannelRouteEndpointIDs(unique))
}

func (m *MemStore) CurrentManagedRealtimeChannelRouteGeneration(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.realtimeChannelRouteGeneration, nil
}

func (m *MemStore) ReplaceManagedRealtimeChannelRoutes(_ context.Context, nodeID string, snapshotGeneration int64, routes []ManagedRealtimeChannelRoute) error {
	if nodeID == "" || snapshotGeneration < 0 {
		return errors.New("state: node id and non-negative route snapshot generation are required")
	}
	unique, err := uniqueManagedRealtimeChannelRoutes(routes)
	if err != nil {
		return err
	}
	for _, route := range unique {
		if route.NodeID != nodeID {
			return errors.New("state: route snapshot contains a different node id")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureRealtimeChannelRouteMapsLocked()
	for route := range m.realtimeChannelRoutes {
		if route.NodeID == nodeID {
			delete(m.realtimeChannelRoutes, route)
			m.realtimeChannelRouteCounts[route.EndpointID]--
			if m.realtimeChannelRouteCounts[route.EndpointID] <= 0 {
				delete(m.realtimeChannelRouteCounts, route.EndpointID)
			}
		}
	}
	for _, route := range unique {
		overflow, disabled := m.realtimeChannelRouteOverflow[route.EndpointID]
		if disabled && ((overflow.OverflowAll && !overflow.Rebuilding) || managedRealtimeChannelRouteChannelOverflowed(overflow, route.Channel)) {
			continue
		}
		m.addRealtimeChannelRouteLocked(route)
	}
	if err := m.enforceMemRealtimeChannelRouteLimitLocked(managedRealtimeChannelRouteEndpointIDs(unique)); err != nil {
		return err
	}
	m.realtimeChannelRouteSnapshots[nodeID] = snapshotGeneration
	now := time.Now()
	for endpointID, overflow := range m.realtimeChannelRouteOverflow {
		if overflow.Rebuilding && overflow.RebuildGeneration <= snapshotGeneration {
			overflow.RebuildStartedAt = now
			m.realtimeChannelRouteOverflow[endpointID] = overflow
		}
	}
	return nil
}

func (m *MemStore) BeginManagedRealtimeChannelRouteRebuild(_ context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureRealtimeChannelRouteMapsLocked()
	now := time.Now()
	var eligible []string
	for endpointID, overflow := range m.realtimeChannelRouteOverflow {
		if (!overflow.Rebuilding && !overflow.NextRebuildAt.After(now)) ||
			(overflow.Rebuilding && now.Sub(overflow.RebuildStartedAt) >= managedRealtimeChannelRouteLockLease) {
			eligible = append(eligible, endpointID)
		}
	}
	if len(eligible) == 0 {
		return false, nil
	}
	m.realtimeChannelRouteGeneration++
	for _, endpointID := range eligible {
		overflow := m.realtimeChannelRouteOverflow[endpointID]
		overflow.Rebuilding = true
		overflow.OverflowAll = true
		overflow.OverflowChannels = make(map[string]struct{})
		overflow.RebuildGeneration = m.realtimeChannelRouteGeneration
		overflow.RebuildStartedAt = now
		overflow.NextRebuildAt = now.Add(managedRealtimeChannelRouteLockLease)
		m.realtimeChannelRouteOverflow[endpointID] = overflow
	}
	return true, nil
}

func (m *MemStore) ListManagedRealtimeChannelRouteView(_ context.Context, endpointID, channel string) (ManagedRealtimeChannelRouteView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	view := ManagedRealtimeChannelRouteView{NodeIDs: make([]string, 0), ReadyNodeIDs: make([]string, 0)}
	if endpointID == "" || channel == "" {
		return view, nil
	}
	if overflow, ok := m.realtimeChannelRouteOverflow[endpointID]; ok {
		view.Disabled = overflow.OverflowAll || managedRealtimeChannelRouteChannelOverflowed(overflow, channel)
	}
	nodes := make(map[string]struct{})
	for route := range m.realtimeChannelRoutes {
		if route.EndpointID == endpointID && route.Channel == channel {
			nodes[route.NodeID] = struct{}{}
		}
	}
	for nodeID := range nodes {
		view.NodeIDs = append(view.NodeIDs, nodeID)
	}
	for nodeID, generation := range m.realtimeChannelRouteSnapshots {
		if generation >= m.realtimeChannelRouteGeneration {
			view.ReadyNodeIDs = append(view.ReadyNodeIDs, nodeID)
		}
	}
	sort.Strings(view.NodeIDs)
	sort.Strings(view.ReadyNodeIDs)
	return view, nil
}

func (m *MemStore) ListManagedRealtimeChannelPublishTargets(_ context.Context, endpointID, channel string) (ManagedRealtimeChannelPublishTargetView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	view := ManagedRealtimeChannelPublishTargetView{Targets: make([]ManagedRealtimeChannelPublishTarget, 0)}
	routedNodes := make(map[string]struct{})
	if endpointID != "" && channel != "" {
		if overflow, ok := m.realtimeChannelRouteOverflow[endpointID]; ok {
			view.Disabled = overflow.OverflowAll || managedRealtimeChannelRouteChannelOverflowed(overflow, channel)
		}
		for route := range m.realtimeChannelRoutes {
			if route.EndpointID == endpointID && route.Channel == channel {
				routedNodes[route.NodeID] = struct{}{}
			}
		}
	}
	for _, node := range m.computeNodes {
		if !node.Active {
			continue
		}
		view.HasActiveNodes = true
		snapshotGeneration, hasSnapshot := m.realtimeChannelRouteSnapshots[node.ID]
		snapshotReady := endpointID != "" && channel != "" && hasSnapshot && snapshotGeneration >= m.realtimeChannelRouteGeneration
		_, hasRoute := routedNodes[node.ID]
		if !view.Disabled && !hasRoute && snapshotReady {
			continue
		}
		target := ManagedRealtimeChannelPublishTarget{
			NodeID: node.ID, NodeName: node.Name, GatewayTargetURL: node.GatewayTargetURL,
			SnapshotReady: snapshotReady,
		}
		view.Targets = append(view.Targets, target)
	}
	sort.Slice(view.Targets, func(i, j int) bool { return view.Targets[i].NodeName < view.Targets[j].NodeName })
	return view, nil
}

func (m *MemStore) FinalizeManagedRealtimeChannelRouteRebuild(ctx context.Context) (bool, error) {
	m.mu.Lock()
	m.ensureRealtimeChannelRouteMapsLocked()
	var inactiveNodeIDs []string
	for _, node := range m.computeNodes {
		if !node.Active {
			inactiveNodeIDs = append(inactiveNodeIDs, node.ID)
		}
	}
	m.mu.Unlock()
	for _, nodeID := range inactiveNodeIDs {
		lock, err := m.AcquireManagedRealtimeChannelRouteLock(ctx, nodeID)
		if err != nil {
			return false, err
		}
		m.mu.Lock()
		if node, ok := m.computeNodes[nodeID]; ok && !node.Active {
			for route := range m.realtimeChannelRoutes {
				if route.NodeID == nodeID {
					delete(m.realtimeChannelRoutes, route)
					m.realtimeChannelRouteCounts[route.EndpointID]--
					if m.realtimeChannelRouteCounts[route.EndpointID] <= 0 {
						delete(m.realtimeChannelRouteCounts, route.EndpointID)
					}
				}
			}
			delete(m.realtimeChannelRouteSnapshots, nodeID)
		}
		m.mu.Unlock()
		lock.Release(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureRealtimeChannelRouteMapsLocked()
	for _, node := range m.computeNodes {
		if node.Active && m.realtimeChannelRouteSnapshots[node.ID] < m.realtimeChannelRouteGeneration {
			return false, nil
		}
	}
	var rebuildingEndpoints []string
	for endpointID, overflow := range m.realtimeChannelRouteOverflow {
		if overflow.Rebuilding {
			rebuildingEndpoints = append(rebuildingEndpoints, endpointID)
		}
	}
	if err := m.enforceMemRealtimeChannelRouteLimitLocked(rebuildingEndpoints); err != nil {
		return false, err
	}
	now := time.Now()
	for endpointID, overflow := range m.realtimeChannelRouteOverflow {
		if !overflow.Rebuilding {
			continue
		}
		if m.realtimeChannelRouteCounts[endpointID] <= managedRealtimeChannelRouteLimit && len(overflow.OverflowChannels) == 0 {
			delete(m.realtimeChannelRouteOverflow, endpointID)
			continue
		}
		overflow.OverflowAll = false
		overflow.Rebuilding = false
		overflow.RebuildStartedAt = time.Time{}
		overflow.NextRebuildAt = now.Add(managedRealtimeChannelRouteRebuildDelay)
		m.realtimeChannelRouteOverflow[endpointID] = overflow
	}
	return true, nil
}

func (m *MemStore) ensureRealtimeChannelRouteMapsLocked() {
	if m.realtimeChannelRoutes == nil {
		m.realtimeChannelRoutes = make(map[ManagedRealtimeChannelRoute]struct{})
	}
	if m.realtimeChannelRouteCounts == nil {
		m.realtimeChannelRouteCounts = make(map[string]int)
	}
	if m.realtimeChannelRouteOverflow == nil {
		m.realtimeChannelRouteOverflow = make(map[string]managedRealtimeChannelRouteOverflowState)
	}
	if m.realtimeChannelRouteSnapshots == nil {
		m.realtimeChannelRouteSnapshots = make(map[string]int64)
	}
}

func (m *MemStore) addRealtimeChannelRouteLocked(route ManagedRealtimeChannelRoute) {
	if _, ok := m.realtimeChannelRoutes[route]; ok {
		return
	}
	m.realtimeChannelRoutes[route] = struct{}{}
	m.realtimeChannelRouteCounts[route.EndpointID]++
}

func (m *MemStore) enforceMemRealtimeChannelRouteLimitLocked(endpointIDs []string) error {
	for _, endpointID := range endpointIDs {
		routeCount := m.realtimeChannelRouteCounts[endpointID]
		if routeCount <= managedRealtimeChannelRouteLimit {
			continue
		}
		overflow, disabled := m.realtimeChannelRouteOverflow[endpointID]
		if !disabled {
			overflow = managedRealtimeChannelRouteOverflowState{
				OverflowChannels: make(map[string]struct{}),
				NextRebuildAt:    time.Now().Add(managedRealtimeChannelRouteRebuildDelay),
			}
		}
		if overflow.OverflowAll && !overflow.Rebuilding {
			m.clearMemRealtimeChannelRoutesLocked(endpointID)
			m.realtimeChannelRouteOverflow[endpointID] = overflow
			continue
		}
		if overflow.OverflowChannels == nil {
			overflow.OverflowChannels = make(map[string]struct{})
		}
		type channelCount struct {
			channel string
			count   int
		}
		countsByChannel := make(map[string]int)
		for route := range m.realtimeChannelRoutes {
			if route.EndpointID == endpointID {
				countsByChannel[route.Channel]++
			}
		}
		channels := make([]channelCount, 0, len(countsByChannel))
		for channel, count := range countsByChannel {
			channels = append(channels, channelCount{channel: channel, count: count})
		}
		sort.Slice(channels, func(i, j int) bool {
			if channels[i].count == channels[j].count {
				return channels[i].channel < channels[j].channel
			}
			return channels[i].count > channels[j].count
		})
		toRemove := routeCount - managedRealtimeChannelRouteLimit
		selectedChannels := make(map[string]struct{})
		for _, item := range channels {
			if toRemove <= 0 {
				break
			}
			overflow.OverflowChannels[item.channel] = struct{}{}
			selectedChannels[item.channel] = struct{}{}
			toRemove -= item.count
		}
		if toRemove > 0 {
			return fmt.Errorf("state: could not compact over-limit realtime channel routes for endpoint %s", endpointID)
		}
		for route := range m.realtimeChannelRoutes {
			if route.EndpointID != endpointID {
				continue
			}
			if _, selected := selectedChannels[route.Channel]; !selected {
				continue
			}
			delete(m.realtimeChannelRoutes, route)
			m.realtimeChannelRouteCounts[endpointID]--
		}
		if m.realtimeChannelRouteCounts[endpointID] <= 0 {
			delete(m.realtimeChannelRouteCounts, endpointID)
		}
		m.realtimeChannelRouteOverflow[endpointID] = overflow
	}
	return nil
}

func managedRealtimeChannelRouteChannelOverflowed(overflow managedRealtimeChannelRouteOverflowState, channel string) bool {
	_, ok := overflow.OverflowChannels[channel]
	return ok
}

func (m *MemStore) clearMemRealtimeChannelRoutesLocked(endpointID string) {
	for route := range m.realtimeChannelRoutes {
		if route.EndpointID == endpointID {
			delete(m.realtimeChannelRoutes, route)
		}
	}
	delete(m.realtimeChannelRouteCounts, endpointID)
}
