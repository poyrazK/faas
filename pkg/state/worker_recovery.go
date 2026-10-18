package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// WorkerRecovery persists worker-pool retries independently of desired capacity.
// A claim serializes app reconciliation; its deadline gates admission, not scale-in.
type WorkerRecovery struct {
	AppID, Revision, ClaimToken, Status  string
	Failures                             int
	NextAttemptAt, LeaseUntil, UpdatedAt time.Time
	Pools                                []WorkerRecoveryPool
}

// WorkerRecoveryPool is the scheduler's last successfully observed plan and
// resident fleet for an environment. Revision fences observations from older
// desired intent, including during mixed-version rollout.
type WorkerRecoveryPool struct {
	Scope        string    `json:"scope"`
	DeploymentID string    `json:"deployment_id"`
	Revision     string    `json:"revision"`
	ObservedAt   time.Time `json:"observed_at"`
	Desired      int       `json:"desired"`
	Running      int       `json:"running"`
	Starting     int       `json:"starting"`
	Draining     int       `json:"draining"`
}

func workerRecoveryFromSQL(r sqlc.WorkerRecovery) (WorkerRecovery, error) {
	var token string
	if r.ClaimToken.Valid {
		token = uuid.UUID(r.ClaimToken.Bytes).String()
	}
	var pools []WorkerRecoveryPool
	if err := json.Unmarshal(r.Pools, &pools); err != nil {
		return WorkerRecovery{}, fmt.Errorf("state: decode worker observations: %w", err)
	}
	return WorkerRecovery{AppID: uuid.UUID(r.AppID.Bytes).String(), Revision: r.Revision,
		ClaimToken: token, Status: r.Status, Failures: int(r.Failures),
		NextAttemptAt: r.NextAttemptAt.Time, LeaseUntil: r.LeaseUntil.Time, UpdatedAt: r.UpdatedAt.Time, Pools: pools}, nil
}

func workerRecoveryUUID(s string) pgtype.UUID {
	id, err := uuid.Parse(s)
	return pgtype.UUID{Bytes: id, Valid: err == nil}
}

func workerRecoveryTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func (s *PgStore) ListWorkerRecoveryApps(ctx context.Context, owner string, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	rows, err := sqlc.New().ListWorkerRecoveryApps(ctx, s.pool, sqlc.ListWorkerRecoveryAppsParams{
		OwnerNodeID: owner, SampledAt: workerRecoveryTime(now), BatchLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("state: list worker recovery: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, id := range rows {
		ids = append(ids, uuid.UUID(id.Bytes).String())
	}
	return ids, nil
}

func (s *PgStore) ClaimWorkerRecovery(ctx context.Context, appID, owner, revision, token string, now, until time.Time) (WorkerRecovery, bool, error) {
	if !validWorkerRecoveryClaim(revision, token, now, until) {
		return WorkerRecovery{}, false, ErrInvalidArgument
	}
	r, err := sqlc.New().ClaimWorkerRecovery(ctx, s.pool, sqlc.ClaimWorkerRecoveryParams{
		AppID: workerRecoveryUUID(appID), OwnerNodeID: owner, Revision: revision, ClaimToken: workerRecoveryUUID(token),
		SampledAt: workerRecoveryTime(now), LeaseUntil: workerRecoveryTime(until)})
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkerRecovery{}, false, nil
	}
	if err != nil {
		return WorkerRecovery{}, false, fmt.Errorf("state: claim worker recovery: %w", err)
	}
	out, err := workerRecoveryFromSQL(r)
	return out, err == nil, err
}

func (s *PgStore) CompleteWorkerRecovery(ctx context.Context, token string, r WorkerRecovery) error {
	if !validWorkerRecoveryCompletion(r) {
		return ErrInvalidArgument
	}
	pools := r.Pools
	if pools == nil {
		pools = []WorkerRecoveryPool{}
	}
	encoded, err := json.Marshal(pools)
	if err != nil {
		return fmt.Errorf("state: encode worker observations: %w", err)
	}
	n, err := sqlc.New().CompleteWorkerRecovery(ctx, s.pool, sqlc.CompleteWorkerRecoveryParams{
		AppID: workerRecoveryUUID(r.AppID), ClaimToken: workerRecoveryUUID(token),
		Revision: r.Revision, Status: r.Status, Failures: int32(r.Failures), NextAttemptAt: workerRecoveryTime(r.NextAttemptAt), UpdatedAt: workerRecoveryTime(r.UpdatedAt), Pools: encoded})
	if err != nil {
		return fmt.Errorf("state: complete worker recovery: %w", err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) WorkerRecoveryByApp(ctx context.Context, id string) (WorkerRecovery, error) {
	r, err := sqlc.New().WorkerRecoveryByApp(ctx, s.pool, workerRecoveryUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkerRecovery{}, ErrNotFound
	}
	if err != nil {
		return WorkerRecovery{}, fmt.Errorf("state: read worker recovery: %w", err)
	}
	return workerRecoveryFromSQL(r)
}

func (m *MemStore) ListWorkerRecoveryApps(_ context.Context, owner string, now time.Time, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, app := range m.apps {
		if app.Status != AppActive || !m.workerRecoveryCandidateLocked(app) || (owner != "" && app.NodeID != "" && app.NodeID != owner) {
			continue
		}
		account, ok := m.accounts[app.AccountID]
		if !ok || !account.Active() {
			continue
		}
		r := m.workerRecovery[id]
		if r.LeaseUntil.After(now) || r.NextAttemptAt.After(now) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.workerRecovery[ids[i]].NextAttemptAt, m.workerRecovery[ids[j]].NextAttemptAt
		if !a.Equal(b) {
			return a.Before(b)
		}
		return ids[i] < ids[j]
	})
	if limit <= 0 {
		return []string{}, nil
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

func (m *MemStore) ClaimWorkerRecovery(_ context.Context, id, owner, revision, token string, now, until time.Time) (WorkerRecovery, bool, error) {
	if !validWorkerRecoveryClaim(revision, token, now, until) {
		return WorkerRecovery{}, false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[id]
	if !ok {
		return WorkerRecovery{}, false, nil
	}
	if app.Status != AppActive || (owner != "" && app.NodeID != "" && app.NodeID != owner) {
		return WorkerRecovery{}, false, nil
	}
	account, ok := m.accounts[app.AccountID]
	if !ok || !account.Active() {
		return WorkerRecovery{}, false, nil
	}
	r := m.workerRecovery[id]
	if r.LeaseUntil.After(now) {
		return WorkerRecovery{}, false, nil
	}
	if r.Revision != revision {
		r.Failures = 0
		r.NextAttemptAt = now.UTC()
		r.Status = "reconciling"
		r.Pools = nil
	}
	r.AppID, r.Revision, r.ClaimToken = id, revision, token
	r.LeaseUntil, r.UpdatedAt = until.UTC(), now.UTC()
	if r.NextAttemptAt.IsZero() {
		r.NextAttemptAt = now.UTC()
	}
	if m.workerRecovery == nil {
		m.workerRecovery = make(map[string]WorkerRecovery)
	}
	m.workerRecovery[id] = r
	r.Pools = slices.Clone(r.Pools)
	return r, true, nil
}

func validWorkerRecoveryClaim(revision, token string, now, until time.Time) bool {
	id, err := uuid.Parse(token)
	return revision != "" && err == nil && id != uuid.Nil && !now.IsZero() && until.After(now)
}

func validWorkerRecoveryCompletion(r WorkerRecovery) bool {
	if r.Failures < 0 || r.Failures > api.WorkerRecoveryFailureCountMax || r.UpdatedAt.IsZero() || r.NextAttemptAt.Before(r.UpdatedAt) {
		return false
	}
	seen := make(map[string]bool, len(r.Pools))
	for _, pool := range r.Pools {
		if api.ValidateScope(pool.Scope) != nil || seen[pool.Scope] || pool.Revision != r.Revision ||
			pool.ObservedAt.IsZero() || pool.ObservedAt.After(r.UpdatedAt) ||
			pool.Desired < 0 || pool.Running < 0 || pool.Starting < 0 || pool.Draining < 0 ||
			(pool.Desired > 0 && pool.DeploymentID == "") {
			return false
		}
		seen[pool.Scope] = true
	}
	switch r.Status {
	case "ready", "starting", "draining", "waiting_capacity", "retrying_startup", "waiting_dependency":
		return true
	default:
		return false
	}
}

func (m *MemStore) CompleteWorkerRecovery(_ context.Context, token string, r WorkerRecovery) error {
	if !validWorkerRecoveryCompletion(r) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.workerRecovery[r.AppID]
	if !ok || old.ClaimToken != token || token == "" || old.Revision != r.Revision || !old.LeaseUntil.After(r.UpdatedAt) {
		return ErrConflict
	}
	old.Status, old.Failures, old.NextAttemptAt, old.UpdatedAt = r.Status, r.Failures, r.NextAttemptAt.UTC(), r.UpdatedAt.UTC()
	old.Pools = slices.Clone(r.Pools)
	old.ClaimToken, old.LeaseUntil = "", time.Unix(0, 0).UTC()
	m.workerRecovery[r.AppID] = old
	return nil
}

func (m *MemStore) WorkerRecoveryByApp(_ context.Context, id string) (WorkerRecovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.workerRecovery[id]
	if !ok {
		return WorkerRecovery{}, ErrNotFound
	}
	r.Pools = slices.Clone(r.Pools)
	return r, nil
}

// Called only while holding m.mu. Captured environments can remain workers
// after the mutable app head switches mode; pending stops also need a sweep.
func (m *MemStore) workerRecoveryCandidateLocked(app App) bool {
	if app.Manifest.ExecutionMode == api.ExecutionModeWorker || app.WorkloadClass == WorkloadClassWorker {
		return true
	}
	for _, dep := range m.deployments {
		if dep.AppID != app.ID || dep.Status != DeployLive {
			continue
		}
		spec, ok := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[dep.ID]]
		if ok && spec.AppID == app.ID && (spec.Settings.Manifest.ExecutionMode == api.ExecutionModeWorker || spec.Settings.WorkloadClass == WorkloadClassWorker) {
			return true
		}
	}
	for _, ins := range m.instances {
		if ins.AppID == app.ID && ins.Mode == string(InstanceModeWorker) && State(ins.State).CountsForRAM() {
			return true
		}
	}
	return false
}
