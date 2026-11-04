package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Edge-rule convergence liveness (ADR-091 amendment 2026-10-27). Each
// gatewayd-internal stamps compute_nodes.edge_listener_heartbeat_at from its
// pg_notify loop; apid's convergence barrier stops waiting for a gateway whose
// stamp is older than EdgeListenerStaleAfter. A NULL stamp (a gateway release
// that predates the column) is still waited for.
const (
	EdgeListenerHeartbeatInterval = 10 * time.Second
	EdgeListenerStaleAfter        = 30 * time.Second
)

// EdgeListenerHeartbeatStore records that a node's edge-rule listener is
// alive. The timestamp is the database's clock.
type EdgeListenerHeartbeatStore interface {
	TouchEdgeListenerHeartbeat(ctx context.Context, nodeName string) error
}

// StaleEdgeListenerStore names the active nodes whose edge-rule listener
// last reported more than staleAfter ago. Nodes that never reported are not
// stale.
type StaleEdgeListenerStore interface {
	StaleEdgeListenerNodes(ctx context.Context, staleAfter time.Duration) ([]string, error)
}

// EdgeRuleAccountMutationLocker serializes edge-rule mutations per account
// across apid replicas, then per app. Two apps of one account can raise
// fences on the same host, and a gateway releases a fence on the newest
// generation's apply, so one account's generations must commit in order.
type EdgeRuleAccountMutationLocker interface {
	AcquireEdgeRuleAccountMutationLocks(ctx context.Context, accountIDs, appIDs []string) (func(context.Context), error)
}

func (s *PgStore) TouchEdgeListenerHeartbeat(ctx context.Context, nodeName string) error {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return errors.New("state: edge listener heartbeat requires a node name")
	}
	if err := sqlc.New().TouchEdgeListenerHeartbeat(ctx, s.pool, nodeName); err != nil {
		return fmt.Errorf("state: edge listener heartbeat for %q: %w", nodeName, err)
	}
	return nil
}

func (s *PgStore) StaleEdgeListenerNodes(ctx context.Context, staleAfter time.Duration) ([]string, error) {
	nodes, err := sqlc.New().StaleEdgeListenerNodes(ctx, s.pool, pgtype.Interval{Microseconds: staleAfter.Microseconds(), Valid: true})
	if err != nil {
		return nil, fmt.Errorf("state: list stale edge listeners: %w", err)
	}
	return nodes, nil
}

func (m *MemStore) TouchEdgeListenerHeartbeat(_ context.Context, nodeName string) error {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return errors.New("state: edge listener heartbeat requires a node name")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.computeNodes {
		if n.Name == nodeName {
			if m.edgeListenerHeartbeats == nil {
				m.edgeListenerHeartbeats = map[string]time.Time{}
			}
			m.edgeListenerHeartbeats[nodeName] = time.Now()
			return nil
		}
	}
	return nil
}

func (m *MemStore) StaleEdgeListenerNodes(_ context.Context, staleAfter time.Duration) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-staleAfter)
	var out []string
	for _, n := range m.computeNodes {
		if at, ok := m.edgeListenerHeartbeats[n.Name]; ok && n.Active && at.Before(cutoff) {
			out = append(out, n.Name)
		}
	}
	slices.Sort(out)
	return out, nil
}

// SetEdgeListenerHeartbeatForTest backdates a node's edge listener stamp.
func (m *MemStore) SetEdgeListenerHeartbeatForTest(nodeName string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.edgeListenerHeartbeats == nil {
		m.edgeListenerHeartbeats = map[string]time.Time{}
	}
	m.edgeListenerHeartbeats[nodeName] = at
}
