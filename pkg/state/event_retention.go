package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventRetentionStore interface {
	GetEventRetentionHealth(context.Context, string, string, api.EventRetentionQuery, time.Time) (api.EventRetentionHealth, error)
}

func retentionQuery(account, app string, q *api.EventRetentionQuery, now time.Time) error {
	if _, err := uuid.Parse(account); err != nil {
		return ErrInvalidArgument
	}
	if app != "" {
		if _, err := uuid.Parse(app); err != nil {
			return ErrInvalidArgument
		}
	}
	if now.IsZero() || q.Validate() != nil {
		return ErrInvalidArgument
	}
	return nil
}

func finishEventRetention(out *api.EventRetentionHealth, limit int) {
	if len(out.Sample) > limit {
		out.SampleTruncated = true
		out.Sample = out.Sample[:limit]
	}
	if out.Sample == nil {
		out.Sample = []api.EventRetentionSample{}
	}
	if out.Storage.Limits.RetainedEvents > 0 {
		out.StorageCountUtilizationPct = 100 * float64(out.Storage.RetainedEvents) / float64(out.Storage.Limits.RetainedEvents)
	}
	if out.Storage.Limits.RetainedBytes > 0 {
		out.StorageBytesUtilizationPct = 100 * float64(out.Storage.RetainedBytes) / float64(out.Storage.Limits.RetainedBytes)
	}
	out.StorageUtilizationPct = max(out.StorageCountUtilizationPct, out.StorageBytesUtilizationPct)
}

func (s *PgStore) GetEventRetentionHealth(ctx context.Context, account, app string, query api.EventRetentionQuery, now time.Time) (api.EventRetentionHealth, error) {
	var out api.EventRetentionHealth
	if err := retentionQuery(account, app, &query, now); err != nil {
		return out, err
	}
	if app != "" {
		app = canonicalMemUUID(app)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	usage, err := q.EventStoragePublicUsage(ctx, tx, mustPgUUID(account))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	limits, ok := api.LimitsFor(api.Plan(usage.Plan))
	if !ok {
		return out, ErrInvalidArgument
	}
	if app != "" {
		if _, err = q.EventRecoveryListApp(ctx, tx, sqlc.EventRecoveryListAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
			return out, mapErr(err)
		}
	}
	r, err := q.EventRetentionHealth(ctx, tx, sqlc.EventRetentionHealthParams{AccountID: mustPgUUID(account), AppID: app, EventSource: query.Source, NowAt: pgtypeFromTime(now), WindowEnd: pgtypeFromTime(now.Add(query.Window)), JobCutoffAt: pgtypeFromTime(now.Add(-api.EventReplayBackfillJobRetention)), RetentionSeconds: int64(PublishedEventIdentityRetention / time.Second), SampleLimit: int32(query.Limit + 1)})
	if err != nil {
		return out, err
	}
	out = api.EventRetentionHealth{ObservedAt: now, WindowSeconds: int64(query.Window / time.Second), EventSource: query.Source, AppID: app, RetainedReceipts: r.RetainedReceipts, RetainedBytes: r.RetainedBytes, UnsettledReceipts: r.UnsettledReceipts, UnknownDeadlineReceipts: r.UnknownDeadlineReceipts, HeldReceipts: r.HeldReceipts, RecoveryHolds: r.RecoveryHolds, RunningBackfillHolds: r.RunningBackfillHolds, RetryableBackfillHolds: r.RetryableBackfillHolds, HeldDueReceipts: r.HeldDueReceipts, EligibleForPruning: r.EligibleForPruning, ExpiringReceipts: r.ExpiringReceipts, Storage: api.EventStorageUsageResponse{RetainedEvents: usage.RetainedEvents, RetainedBytes: usage.RetainedBytes, PendingEvents: usage.PendingEvents, OldestPendingAt: eventStorageTime(usage.OldestPendingAt), Limits: limits.EventStorage}}
	if err = json.Unmarshal(r.Sample, &out.Sample); err != nil {
		return out, err
	}
	finishEventRetention(&out, query.Limit)
	return out, tx.Commit(ctx)
}

func (m *MemStore) GetEventRetentionHealth(ctx context.Context, account, app string, query api.EventRetentionQuery, now time.Time) (api.EventRetentionHealth, error) {
	var out api.EventRetentionHealth
	if err := retentionQuery(account, app, &query, now); err != nil {
		return out, err
	}
	if app != "" {
		app = canonicalMemUUID(app)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	usage, err := m.eventStorageUsageLocked(account)
	if err != nil {
		return out, err
	}
	if app != "" && !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	out = api.EventRetentionHealth{ObservedAt: now, WindowSeconds: int64(query.Window / time.Second), EventSource: query.Source, AppID: app, Storage: usage, Sample: []api.EventRetentionSample{}}
	holds := m.eventRecoveryReceiptHoldsLocked(account, now)
	for _, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var identity publishedEventIdentity
		if json.Unmarshal(work.Payload, &identity) != nil || !sameMemUUID(eventRoutingAccount(work), account) || query.Source != "" && query.Source != identity.Source {
			continue
		}
		if app != "" {
			found := false
			for _, recipient := range work.RecipientSnapshot {
				if sameMemUUID(recipient.AppID, app) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out.RetainedReceipts++
		out.RetainedBytes += work.StorageBytes
		if !work.Delivered {
			out.UnsettledReceipts++
			continue
		}
		if work.DeliveredAt.IsZero() {
			out.UnknownDeadlineReceipts++
			continue
		}
		until := work.DeliveredAt.Add(PublishedEventIdentityRetention)
		status, holdReason := "expiring", ""
		if _, held := holds[work.ID]; held {
			status, holdReason = "held", "recovery_pending"
			out.HeldReceipts++
			out.RecoveryHolds++
			if until.Before(now) {
				out.HeldDueReceipts++
			}
		} else if until.Before(now) {
			out.EligibleForPruning++
			status = "eligible_for_pruning"
		} else if !until.After(now.Add(query.Window)) {
			out.ExpiringReceipts++
		}
		if !until.After(now.Add(query.Window)) {
			appendRetentionSample(&out, api.EventRetentionSample{EventSource: identity.Source, EventID: identity.ID, AcceptedAt: work.CreatedAt, RetainUntil: until, RetainedBytes: work.StorageBytes, Status: status, HoldReason: holdReason}, query.Limit)
		}
	}
	// Memory storage has no backfill job store and therefore no backfill pins.

	finishEventRetention(&out, query.Limit)
	return out, nil
}

func retentionSampleLess(a, b api.EventRetentionSample) bool {
	if !a.RetainUntil.Equal(b.RetainUntil) {
		return a.RetainUntil.Before(b.RetainUntil)
	}
	if a.EventSource != b.EventSource {
		return a.EventSource < b.EventSource
	}
	return a.EventID < b.EventID
}
func appendRetentionSample(out *api.EventRetentionHealth, sample api.EventRetentionSample, limit int) {
	size := limit + 1
	position := sort.Search(len(out.Sample), func(i int) bool { return retentionSampleLess(sample, out.Sample[i]) })
	if position >= size {
		return
	}
	if len(out.Sample) < size {
		out.Sample = append(out.Sample, api.EventRetentionSample{})
	}
	copy(out.Sample[position+1:], out.Sample[position:len(out.Sample)-1])
	out.Sample[position] = sample
}
