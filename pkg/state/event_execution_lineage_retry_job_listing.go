package state

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type lineageRetryJobsPage struct {
	limit     int
	createdAt time.Time
	requestID string
}

func lineageRetryJobsPagination(status, after string, limit int) (lineageRetryJobsPage, error) {
	page := lineageRetryJobsPage{limit: limit, requestID: uuid.Nil.String()}
	if status != "" && status != "pending" && status != "paused" && status != "completed" && status != "cancelled" {
		return page, ErrInvalidArgument
	}
	if limit == 0 {
		page.limit = api.EventExecutionLineageRetryJobsPageDefault
	}
	if page.limit < 1 || page.limit > api.EventExecutionLineageRetryJobsPageMax || len(after) > api.EventExecutionLineageRetryJobsCursorMaxBytes {
		return page, ErrInvalidArgument
	}
	if after == "" {
		return page, nil
	}
	at, id, ok := strings.Cut(after, ",")
	if !ok {
		return page, ErrInvalidArgument
	}
	created, err := time.Parse(time.RFC3339Nano, at)
	if err != nil || created.IsZero() || !created.Equal(created.Truncate(time.Microsecond)) {
		return page, ErrInvalidArgument
	}
	request, err := uuid.Parse(id)
	if err != nil || request == uuid.Nil {
		return page, ErrInvalidArgument
	}
	page.createdAt, page.requestID = created.UTC(), request.String()
	return page, nil
}

func lineageRetryJobsResult(jobs []api.EventExecutionLineageRetryJobSummary, limit int) api.EventExecutionLineageRetryJobs {
	out := api.EventExecutionLineageRetryJobs{Jobs: jobs}
	if len(jobs) > limit {
		out.Jobs = jobs[:limit]
		last := out.Jobs[limit-1]
		out.NextAfter = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "," + last.RequestID
	}
	return out
}

func (s *PgStore) ListEventExecutionLineageRetryJobs(ctx context.Context, account, app, sub, status, after string, limit int, options ...api.EventExecutionLineageRetryJobAttentionOptions) (api.EventExecutionLineageRetryJobs, error) {
	out := api.EventExecutionLineageRetryJobs{Jobs: []api.EventExecutionLineageRetryJobSummary{}}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	attention, err := lineageRetryJobAttentionOption(status, options)
	if err != nil {
		return out, err
	}
	page, err := lineageRetryJobsPagination(status, after, limit)
	if err != nil {
		return out, err
	}
	now := time.Now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	_, err = q.EventConsumerExecutionTarget(ctx, tx, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := q.ListEventExecutionLineageRetryJobs(ctx, tx, sqlc.ListEventExecutionLineageRetryJobsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), Status: status, HasAfter: after != "", AfterCreatedAt: NewPgtypeTime(page.createdAt), AfterRequestID: mustPgUUID(page.requestID), PageLimit: int32(page.limit + 1), AttentionFilter: attention, NowAt: pgtypeFromTime(now), StalledBefore: pgtypeFromTime(now.Add(-api.EventExecutionLineageRetryInactivityWarning)), RetentionBefore: pgtypeFromTime(now.Add(api.EventExecutionLineageRetryRetentionWarning))})
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		out.Jobs = append(out.Jobs, api.EventExecutionLineageRetryJobSummary{RequestID: uuidString(row.RequestID), SubscriptionID: uuidString(row.SubscriptionID), AppID: uuidString(row.AppID), Status: row.State, Selected: int(row.Selected), Processed: int(row.Processed), CreatedAt: timeFromPgtype(row.CreatedAt), UpdatedAt: timeFromPgtype(row.UpdatedAt), RetainUntil: timeFromPgtype(row.RetainUntil), LastProgressAt: timestamptzToTimePtr(row.LastProgressAt)})
	}
	result, err := lineageRetryJobAttentionResult(out.Jobs, page.limit, attention, now)
	if err != nil {
		return out, err
	}
	return result, tx.Commit(ctx)
}

func (m *MemStore) ListEventExecutionLineageRetryJobs(ctx context.Context, account, app, sub, status, after string, limit int, options ...api.EventExecutionLineageRetryJobAttentionOptions) (api.EventExecutionLineageRetryJobs, error) {
	out := api.EventExecutionLineageRetryJobs{Jobs: []api.EventExecutionLineageRetryJobSummary{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	attention, err := lineageRetryJobAttentionOption(status, options)
	if err != nil {
		return out, err
	}
	page, err := lineageRetryJobsPagination(status, after, limit)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	now := time.Now()
	for _, job := range m.eventExecutionCoverage[canonicalMemUUID(sub)].jobs {
		if !job.RetainUntil.After(now) || (status != "" && status != job.Status) || !lineageRetryJobMatchesAttention(job.Status, job.CreatedAt, job.UpdatedAt, job.RetainUntil, attention, now) {
			continue
		}
		if after != "" && (job.CreatedAt.After(page.createdAt) || (job.CreatedAt.Equal(page.createdAt) && job.RequestID >= page.requestID)) {
			continue
		}
		out.Jobs = append(out.Jobs, api.EventExecutionLineageRetryJobSummary{RequestID: job.RequestID, SubscriptionID: job.SubscriptionID, AppID: job.AppID, Status: job.Status, Selected: len(job.Items), Processed: job.Processed, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, RetainUntil: job.RetainUntil, LastProgressAt: copyLineageProgressTime(job.LastProgressAt)})
	}
	sort.Slice(out.Jobs, func(i, j int) bool {
		if out.Jobs[i].CreatedAt.Equal(out.Jobs[j].CreatedAt) {
			return out.Jobs[i].RequestID > out.Jobs[j].RequestID
		}
		return out.Jobs[i].CreatedAt.After(out.Jobs[j].CreatedAt)
	})
	result, err := lineageRetryJobAttentionResult(out.Jobs, page.limit, attention, now)
	if err != nil {
		return out, err
	}
	return result, nil
}
