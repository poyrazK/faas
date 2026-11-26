package pgproxy

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgproto3"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
)

type trackedQuery struct {
	target   dbquerycontrol.Target
	started  time.Time
	canceler *pooledCanceler
}

// inspectableExecution recognizes a single execution in a protocol group.
// The pool never forwards another group while relaying that execution.
// A simple-query packet with several statements has no such boundary.
func inspectableExecution(msg pgproto3.FrontendMessage, pending []expectation) bool {
	switch m := msg.(type) {
	case *pgproto3.Query:
		return len(pending) == 1 && !strings.Contains(strings.TrimSuffix(strings.TrimSpace(m.String), ";"), ";")
	case *pgproto3.Sync, *pgproto3.Flush:
		executions := 0
		for _, e := range pending {
			switch e.kind {
			case 'E':
				executions++
			case 'P', 'B', 'D', 'C', 'S':
			default:
				return false
			}
		}
		return executions == 1
	}
	return false
}

func (c *pooledCanceler) beginQuery() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.srv == nil || c.srv.ended.Load() || c.activeQuery.ID != "" {
		return
	}
	key := c.srv.key
	if key.pid == 0 || key.pid > math.MaxInt32 || key.target.PrimaryEpochMicros <= 0 || key.target.replicaOnly {
		return
	}
	target := dbquerycontrol.Target{
		ID: uuid.NewString(), DatabaseID: key.databaseID, NodeID: key.target.Node,
		PrimaryEpochMicros: key.target.PrimaryEpochMicros, BackendPID: int32(key.pid), ApplicationName: c.applicationName,
	}
	if !target.Valid() {
		return
	}
	c.activeQuery = target
	c.p.queryMu.Lock()
	if c.p.queries == nil {
		c.p.queries = make(map[string]trackedQuery)
	}
	c.p.queries[target.ID] = trackedQuery{target: target, started: time.Now(), canceler: c}
	c.p.queryMu.Unlock()
}

func (c *pooledCanceler) endQuery() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endQueryLocked()
}

// The canceler mutex also gates forwarding the next client message and
// returning the borrowed backend. Operator and client cancels share it.
func (c *pooledCanceler) endQueryLocked() {
	if c.activeQuery.ID == "" {
		return
	}
	c.p.queryMu.Lock()
	delete(c.p.queries, c.activeQuery.ID)
	c.p.queryMu.Unlock()
	c.activeQuery = dbquerycontrol.Target{}
}

func (p *Proxy) cancellableQueries() []*scheddpb.DatabaseQueryTarget {
	p.queryMu.Lock()
	active := make([]trackedQuery, 0, len(p.queries))
	for _, q := range p.queries {
		active = append(active, q)
	}
	p.queryMu.Unlock()
	sort.Slice(active, func(i, j int) bool { return active[i].started.Before(active[j].started) })
	active = active[:min(len(active), dbquerycontrol.MaxTargets)]
	result := make([]*scheddpb.DatabaseQueryTarget, 0, len(active))
	for _, q := range active {
		result = append(result, q.target.Proto())
	}
	return result
}

func (p *Proxy) cancelObservedQuery(ctx context.Context, target dbquerycontrol.Target) string {
	p.queryMu.Lock()
	q, found := p.queries[target.ID]
	p.queryMu.Unlock()
	if !found || q.target != target {
		return dbquerycontrol.Stale
	}
	c := q.canceler
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeQuery != target || c.srv == nil || c.srv.ended.Load() {
		return dbquerycontrol.Stale
	}
	if ctx.Err() != nil {
		return dbquerycontrol.Unconfirmed
	}
	c.endQueryLocked()
	signal, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if err := p.cancelBackend(signal, c.srv.key); err != nil {
		return dbquerycontrol.Unconfirmed
	}
	return dbquerycontrol.SignalSent
}

// RunQueryControls exchanges current query generations and one-shot
// commands over pgproxyd's existing authenticated scheduler connection.
func (p *Proxy) RunQueryControls(ctx context.Context, client scheddpb.ScheddClient) {
	owner := uuid.NewString()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var results []*scheddpb.DatabaseQueryControlResult
	for {
		exchange, cancel := context.WithTimeout(ctx, 2*time.Second)
		response, err := client.ExchangeDatabaseQueryControls(exchange, &scheddpb.ExchangeDatabaseQueryControlsRequest{
			OwnerId: owner, Active: p.cancellableQueries(), Results: results,
		})
		cancel()
		if err == nil {
			p.queryControls.Store(true)
			results = results[:0]
			for _, command := range response.GetCancel() {
				if ctx.Err() != nil {
					return
				}
				target := dbquerycontrol.FromProto(command.GetTarget())
				outcome := dbquerycontrol.Unconfirmed
				if expires := command.GetExpiresAt(); expires.CheckValid() == nil {
					execution, cancel := context.WithDeadline(ctx, expires.AsTime())
					outcome = p.cancelObservedQuery(execution, target)
					cancel()
				}
				results = append(results, &scheddpb.DatabaseQueryControlResult{TargetId: target.ID, Outcome: outcome})
				p.log.Info("pgproxy: operator query cancel", "database_id", target.DatabaseID, "target_id", target.ID, "outcome", outcome)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
