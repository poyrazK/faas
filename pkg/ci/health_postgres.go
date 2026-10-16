package ci

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) CIHealth(ctx context.Context, account string) (Health, error) {
	if !ValidID(account) {
		return Health{}, ErrNotFound
	}
	return s.readHealth(ctx, account, false)
}

// CIFleetHealth is used only by the private daemon metrics listener. HTTP
// handlers must use CIHealth, which rejects an empty or noncanonical account.
func (s *PostgresStore) CIFleetHealth(ctx context.Context) (Health, error) {
	return s.readHealth(ctx, uuid.Nil.String(), true)
}

func (s *PostgresStore) readHealth(ctx context.Context, account string, fleet bool) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, api.CIHealthTimeoutSeconds*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Health{}, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now, err := s.q.CIHealthNow(ctx, tx)
	if err != nil {
		return Health{}, storeError(err)
	}
	queues, err := s.q.CIHealthQueues(ctx, tx, sqlc.CIHealthQueuesParams{AccountID: dbUUID(account), Fleet: fleet, OrphanHours: api.CIStorageOrphanHours})
	if err != nil {
		return Health{}, storeError(err)
	}
	usage, err := s.q.CIHealthUsage(ctx, tx, sqlc.CIHealthUsageParams{AccountID: dbUUID(account), Fleet: fleet})
	if err != nil {
		return Health{}, storeError(err)
	}
	workers, err := s.q.CIHealthWorkers(ctx, tx)
	if err != nil {
		return Health{}, storeError(err)
	}
	out := Health{ObservedAt: now.Time, Queues: make([]QueueHealth, 0, len(queues)), Quotas: make([]QuotaHealth, 0, len(usage)), Workers: summarizeWorkers(now.Time, workers)}
	for _, q := range queues {
		out.Queues = append(out.Queues, QueueHealth{Name: q.QQueue, Pending: q.Pending, InFlight: q.Inflight, Dead: q.Dead, Due: q.Due, StaleLeases: q.StaleLeases, OldestItemAgeSeconds: q.OldestItemAgeSeconds})
	}
	for _, u := range usage {
		q := QuotaHealth{Resource: u.Resource, Used: u.Used}
		if limit, ok := healthQuotaLimit(u.Resource, fleet); ok {
			q.Limit = &limit
			headroom := max(0, limit-u.Used)
			q.Headroom = &headroom
		}
		out.Quotas = append(out.Quotas, q)
	}
	if err := tx.Commit(ctx); err != nil {
		return Health{}, storeError(err)
	}
	return out, nil
}

func healthQuotaLimit(resource string, fleet bool) (int64, bool) {
	if fleet {
		limits := map[string]int64{"active_executions": api.CIActiveExecutionsGlobal, "dispatch_leases": api.CIDispatchWorkers,
			"storage_objects": api.CIStorageMaxObjectsGlobal, "storage_bytes": api.CIStorageMaxBytesGlobal, "storage_journal": api.CIStorageMaxJournalGlobal, "storage_io": api.CIStorageMaxIOGlobal}
		limit, ok := limits[resource]
		return limit, ok
	}
	limits := map[string]int64{"execution_details": api.CIMaxJobsExecutionsPerAccount, "active_executions": api.CIActiveExecutionsPerAccount,
		"dispatch_leases": api.CIDispatchLeasesPerAccount, "archives": api.CIMaxExecutionArchivesPerAccount, "profiles": api.CIMaxExecutionProfilesPerAccount,
		"revisions": api.CIMaxRevisionsPerAccount, "metadata_runs": api.CIMaxRunsPerAccount, "source_events": api.CIGitHubMaxTriggersPerAccount,
		"storage_objects": api.CIStorageMaxObjectsPerAccount, "storage_bytes": api.CIStorageMaxBytesPerAccount, "storage_journal": api.CIStorageMaxJournalPerAccount, "storage_io": api.CIStorageMaxIOPerAccount}
	limit, ok := limits[resource]
	return limit, ok
}

func summarizeWorkers(now time.Time, rows []sqlc.CiWorkerHealth) []WorkerHealth {
	out := make([]WorkerHealth, 0, len(workerKinds()))
	for _, kind := range workerKinds() {
		h := WorkerHealth{Name: kind, Status: "unknown", Replicas: make(map[string]int64)}
		for _, condition := range workerConditions() {
			h.Replicas[condition] = 0
		}
		for _, row := range rows {
			if row.Worker != string(kind) {
				continue
			}
			condition := workerCondition(now, row)
			h.Replicas[condition]++
			if h.LastHeartbeatAt == nil || row.HeartbeatAt.Time.After(*h.LastHeartbeatAt) {
				t := row.HeartbeatAt.Time
				h.LastHeartbeatAt = &t
			}
			if row.CycleAt.Valid && (h.LastCycleAt == nil || row.CycleAt.Time.After(*h.LastCycleAt)) {
				t := row.CycleAt.Time
				h.LastCycleAt = &t
			}
		}
		// This is role availability, not an assertion that every replica is healthy.
		// The per-condition counts retain partial failures and old process evidence.
		for _, condition := range []string{"healthy", "degraded", "starting", "stalled", "blocked", "disabled", "stale"} {
			if h.Replicas[condition] > 0 {
				h.Status = condition
				break
			}
		}
		out = append(out, h)
	}
	return out
}

func workerCondition(now time.Time, row sqlc.CiWorkerHealth) string {
	if now.Sub(row.HeartbeatAt.Time) > api.CIWorkerHeartbeatStaleSeconds*time.Second {
		return "stale"
	}
	if !row.Enabled {
		return "disabled"
	}
	if !row.Ready {
		return "blocked"
	}
	progress := row.StartedAt.Time
	if row.CycleAt.Valid {
		progress = row.CycleAt.Time
	}
	if now.Sub(progress) > api.CIWorkerProgressStaleSeconds*time.Second {
		return "stalled"
	}
	if !row.CycleAt.Valid {
		return "starting"
	}
	if row.LastError != "" {
		return "degraded"
	}
	return "healthy"
}

func (s *PostgresStore) claimWorkerHealth(ctx context.Context, kind WorkerKind, token string, enabled, ready bool, sequence int64) (workerHealthLease, error) {
	if !validWorker(kind) || !ValidID(token) {
		return workerHealthLease{}, ErrConflict
	}
	slot, err := s.q.ClaimCIWorkerHealthSlot(ctx, s.pool, sqlc.ClaimCIWorkerHealthSlotParams{Worker: string(kind), Token: dbUUID(token), Sequence: sequence, Enabled: enabled, Ready: ready, MaxSlots: api.CIWorkerHealthSlots, ExpirySeconds: api.CIWorkerSlotExpirySeconds})
	if err != nil {
		return workerHealthLease{}, storeError(err)
	}
	return workerHealthLease{kind: kind, slot: slot, token: token}, nil
}

func (s *PostgresStore) pulseWorkerHealth(ctx context.Context, lease workerHealthLease, sequence int64, failed bool) error {
	n, err := s.q.PulseCIWorkerHealth(ctx, s.pool, sqlc.PulseCIWorkerHealthParams{Worker: string(lease.kind), Slot: lease.slot, Token: dbUUID(lease.token), Sequence: sequence, Failed: failed, ExpirySeconds: api.CIWorkerSlotExpirySeconds})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
