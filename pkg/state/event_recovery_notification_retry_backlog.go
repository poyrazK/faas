package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventRecoveryNotificationRetryBacklogStore interface {
	ListEventRecoveryNotificationRetryBacklog(context.Context, string, string, api.EventRecoveryNotificationRetryBacklogQuery, time.Time) (api.EventRecoveryNotificationRetryBacklog, error)
}

func normalizeNotificationRetryBacklog(account, app string, query api.EventRecoveryNotificationRetryBacklogQuery) (api.EventRecoveryNotificationRetryBacklogQuery, recoveryListCursor, error) {
	var cursor recoveryListCursor
	if err := eventRecoveryIDs(account, app); err != nil {
		return query, cursor, err
	}
	if err := query.Normalize(); err != nil {
		return query, cursor, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	cursor = recoveryListCursor{Account: canonicalMemUUID(account), App: canonicalMemUUID(app), Filters: "notification-retry-backlog:" + query.Status}
	if query.Cursor == "" {
		return query, cursor, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(query.Cursor)
	var got recoveryListCursor
	if err != nil || json.Unmarshal(raw, &got) != nil || got.Account != cursor.Account || got.App != cursor.App || got.Filters != cursor.Filters || got.Created.IsZero() {
		return query, cursor, ErrEventRecoveryQuery
	}
	parsed, err := uuid.Parse(got.ID)
	if err != nil || parsed == uuid.Nil || parsed.String() != got.ID {
		return query, cursor, ErrEventRecoveryQuery
	}
	return query, got, nil
}

type notificationRetryBacklogJob struct {
	ID        string
	CreatedAt time.Time
}

func notificationRetryBacklogPage(app string, now time.Time, jobs []notificationRetryBacklogJob, query api.EventRecoveryNotificationRetryBacklogQuery, cursor recoveryListCursor) (api.EventRecoveryNotificationRetryBacklog, []notificationRetryBacklogJob) {
	out := api.EventRecoveryNotificationRetryBacklog{AppID: canonicalMemUUID(app), ObservedAt: now, CountsScope: "job_page", Requests: []api.EventRecoveryNotificationRetryBacklogRequest{}}
	if len(jobs) > query.PageSize {
		jobs = jobs[:query.PageSize]
		last := jobs[len(jobs)-1]
		cursor.Created, cursor.ID = last.CreatedAt, last.ID
		raw, _ := json.Marshal(cursor)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	out.JobsScanned = len(jobs)
	return out, jobs
}
func notificationRetryBacklogAppend(out *api.EventRecoveryNotificationRetryBacklog, job notificationRetryBacklogJob, history api.EventRecoveryNotificationRetryHistory, statuses []string) {
	// Totals cover every request in the scanned job page, before filtering.
	out.Totals.RequestCount += history.Totals.RequestCount
	out.Totals.SucceededCount += history.Totals.SucceededCount
	out.Totals.FailedCount += history.Totals.FailedCount
	out.Totals.PendingCount += history.Totals.PendingCount
	out.Totals.InconclusiveCount += history.Totals.InconclusiveCount
	out.Totals.IncompleteEvidenceCount += history.Totals.IncompleteEvidenceCount
	history.ApplyStatusFilter(statuses)
	for _, summary := range history.Decisions {
		base := "/v1/event-recoveries/" + url.PathEscape(job.ID)
		out.Requests = append(out.Requests, api.EventRecoveryNotificationRetryBacklogRequest{JobID: job.ID, JobCreatedAt: job.CreatedAt, Summary: summary, DetailPath: base + "/notification-retry-decisions/" + url.PathEscape(summary.RequestID), RetryPreviewPath: base + "/notifications/retry-preview"})
	}
	out.MatchedCount = len(out.Requests)
}
func (s *PgStore) ListEventRecoveryNotificationRetryBacklog(ctx context.Context, account, app string, query api.EventRecoveryNotificationRetryBacklogQuery, now time.Time) (api.EventRecoveryNotificationRetryBacklog, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryBacklog
	query, cursor, err := normalizeNotificationRetryBacklog(account, app, query)
	if err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.EventRecoveryListApp(ctx, tx, sqlc.EventRecoveryListAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
		return out, mapErr(err)
	}
	id := uuid.Nil.String()
	if cursor.ID != "" {
		id = cursor.ID
	}
	rows, err := q.EventRecoveryNotificationRetryBacklogJobs(ctx, tx, sqlc.EventRecoveryNotificationRetryBacklogJobsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), HasCursor: query.Cursor != "", CursorCreated: pgtypeFromTime(cursor.Created), CursorID: mustPgUUID(id), PageLimit: int32(query.PageSize + 1)})
	if err != nil {
		return out, err
	}
	jobs := make([]notificationRetryBacklogJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, notificationRetryBacklogJob{ID: uuidString(row.ID), CreatedAt: row.CreatedAt.Time})
	}
	out, jobs = notificationRetryBacklogPage(app, now, jobs, query, cursor)
	statuses, _ := api.ParseEventRecoveryNotificationRetryHistoryStatus(query.Status)
	for _, job := range jobs {
		history, err := getEventRecoveryNotificationRetryHistory(ctx, q, tx, account, job.ID, now)
		if err != nil {
			return out, err
		}
		notificationRetryBacklogAppend(&out, job, history, statuses)
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) ListEventRecoveryNotificationRetryBacklog(ctx context.Context, account, app string, query api.EventRecoveryNotificationRetryBacklogQuery, now time.Time) (api.EventRecoveryNotificationRetryBacklog, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryBacklog
	query, cursor, err := normalizeNotificationRetryBacklog(account, app, query)
	if err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	jobs := []notificationRetryBacklogJob{}
	for _, entry := range m.eventRecoveryJobs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if !sameMemUUID(entry.AccountID, account) || !sameMemUUID(entry.Job.AppID, app) || len(entry.NotificationRetryReceipts) == 0 {
			continue
		}
		job := notificationRetryBacklogJob{ID: entry.Job.ID, CreatedAt: entry.Job.CreatedAt}
		if query.Cursor != "" && (job.CreatedAt.After(cursor.Created) || job.CreatedAt.Equal(cursor.Created) && job.ID >= cursor.ID) {
			continue
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID > jobs[j].ID
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	out, jobs = notificationRetryBacklogPage(app, now, jobs, query, cursor)
	statuses, _ := api.ParseEventRecoveryNotificationRetryHistoryStatus(query.Status)
	for _, job := range jobs {
		entry, err := m.eventRecoveryJobLocked(account, job.ID)
		if err != nil {
			return out, err
		}
		history, err := m.getEventRecoveryNotificationRetryHistoryLocked(ctx, account, entry, now)
		if err != nil {
			return out, err
		}
		notificationRetryBacklogAppend(&out, job, history, statuses)
	}
	return out, ctx.Err()
}
