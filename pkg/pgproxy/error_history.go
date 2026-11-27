package pgproxy

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgproto3"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbqueryerrors"
)

const errorHistoryPolicyTTL = 12 * time.Second

type errorHistoryPolicy struct {
	epoch     string
	expiresAt time.Time
}

type pooledErrorHistory struct {
	mu       sync.RWMutex
	policies map[string]errorHistoryPolicy
	queue    chan dbqueryerrors.Event
	dropped  atomic.Uint64
}

func (h *pooledErrorHistory) record(runtimeID, role string, e *pgproto3.ErrorResponse) {
	severity := e.SeverityUnlocalized
	if severity == "" {
		severity = e.Severity
	}
	if !dbqueryerrors.ValidSeverity(severity) || !dbqueryerrors.ValidSQLState(e.Code) {
		return
	}
	h.mu.RLock()
	policy := h.policies[runtimeID]
	h.mu.RUnlock()
	now := time.Now()
	if !now.Before(policy.expiresAt) {
		return
	}
	event := dbqueryerrors.Event{
		ID: uuid.NewString(), RuntimeID: runtimeID, Epoch: policy.epoch,
		ObservedAt: now.UTC(), Severity: severity, SQLState: e.Code, RoleName: role,
	}
	select {
	case h.queue <- event:
	default:
		h.dropped.Add(1)
	}
}

func (h *pooledErrorHistory) update(runtimeIDs []string, policies []*scheddpb.DatabaseErrorHistoryPolicy, started time.Time) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, policy := range h.policies {
		if !now.Before(policy.expiresAt) {
			delete(h.policies, id)
		}
	}
	for _, id := range runtimeIDs {
		delete(h.policies, id)
	}
	for _, policy := range policies {
		if dbqueryerrors.ValidID(policy.GetEpoch()) {
			h.policies[policy.GetRuntimeDatabaseId()] = errorHistoryPolicy{epoch: policy.GetEpoch(), expiresAt: started.Add(errorHistoryPolicyTTL)}
		}
	}
}

func (s *poolSet) errorHistoryDatabases() []string {
	s.mu.Lock()
	seen := make(map[string]struct{}, len(s.servers))
	for key := range s.pools {
		seen[key.databaseID] = struct{}{}
	}
	s.mu.Unlock()
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// RunErrorHistory refreshes opt-in and sends bounded reports independently of
// the query path. Retries carry the same event IDs until schedd acknowledges them.
func (p *Proxy) RunErrorHistory(ctx context.Context, client scheddpb.ScheddClient) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var cursor int
	pending := make([]*scheddpb.DatabasePooledError, 0, dbqueryerrors.MaxBatch)
	for ctx.Err() == nil {
		pending = p.drainErrorHistory(pending)
		ids := p.pools.errorHistoryDatabases()
		if len(ids) > dbqueryerrors.MaxDatabaseIDs {
			cursor %= len(ids)
			end := min(cursor+dbqueryerrors.MaxDatabaseIDs, len(ids))
			ids, cursor = ids[cursor:end], end
		} else {
			cursor = 0
		}
		if len(ids) != 0 || len(pending) != 0 {
			started := time.Now()
			call, cancel := context.WithTimeout(ctx, 2*time.Second)
			res, err := client.ExchangeDatabaseErrorHistory(call, &scheddpb.ExchangeDatabaseErrorHistoryRequest{
				RuntimeDatabaseIds: ids, Events: pending,
			})
			cancel()
			if err == nil {
				p.errorHistory.update(ids, res.GetPolicies(), started)
				pending = pending[:0]
			}
		}
		if dropped := p.errorHistory.dropped.Swap(0); dropped != 0 {
			p.log.Warn("pgproxy: database error history queue is full", "events_dropped", dropped)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Proxy) drainErrorHistory(pending []*scheddpb.DatabasePooledError) []*scheddpb.DatabasePooledError {
	for len(pending) < dbqueryerrors.MaxBatch {
		select {
		case event := <-p.errorHistory.queue:
			pending = append(pending, event.Proto())
		default:
			return pending
		}
	}
	return pending
}
