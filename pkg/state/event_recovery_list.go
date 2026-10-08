package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type recoveryListCursor struct {
	Account string    `json:"account"`
	App     string    `json:"app"`
	Filters string    `json:"filters"`
	Created time.Time `json:"created"`
	ID      string    `json:"id"`
}

func recoveryListScope(account, app string, query api.EventRecoveryListQuery) recoveryListCursor {
	query.Cursor = ""
	query.Limit = 0
	filters, _ := json.Marshal(query)
	return recoveryListCursor{Account: canonicalMemUUID(account), App: canonicalMemUUID(app), Filters: string(filters)}
}
func normalizeRecoveryList(account, app string, query api.EventRecoveryListQuery) (api.EventRecoveryListQuery, recoveryListCursor, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return query, recoveryListCursor{}, err
	}
	if err := query.Validate(); err != nil {
		return query, recoveryListCursor{}, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	if query.CreatedAfter != nil {
		t := query.CreatedAfter.UTC()
		query.CreatedAfter = &t
	}
	if query.CreatedBefore != nil {
		t := query.CreatedBefore.UTC()
		query.CreatedBefore = &t
	}
	scope := recoveryListScope(account, app, query)
	if query.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		var cursor recoveryListCursor
		if err != nil || json.Unmarshal(data, &cursor) != nil {
			return query, scope, ErrEventRecoveryQuery
		}
		if cursor.Account != scope.Account || cursor.App != scope.App || cursor.Filters != scope.Filters || cursor.Created.IsZero() {
			return query, scope, ErrEventRecoveryQuery
		}
		id, err := uuid.Parse(cursor.ID)
		if err != nil || id.String() != cursor.ID {
			return query, scope, ErrEventRecoveryQuery
		}
		scope = cursor
	}
	if query.Limit == 0 {
		query.Limit = api.EventRecoveryJobsPageMax
	}
	return query, scope, nil
}
func recoveryListPage(jobs []api.EventRecoveryJob, limit int, cursor recoveryListCursor) api.EventRecoveryJobs {
	out := api.EventRecoveryJobs{Jobs: jobs}
	if len(jobs) > limit {
		out.Jobs = jobs[:limit]
		last := out.Jobs[limit-1]
		cursor.Created = last.CreatedAt
		cursor.ID = last.ID
		data, _ := json.Marshal(cursor)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return out
}
func recoveryListTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtypeFromTime(*t)
}
func (s *PgStore) ListEventRecoveries(ctx context.Context, account, app string, query api.EventRecoveryListQuery) (api.EventRecoveryJobs, error) {
	query, cursor, err := normalizeRecoveryList(account, app, query)
	if err != nil {
		return api.EventRecoveryJobs{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventRecoveryJobs{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	_, err = q.EventRecoveryListApp(ctx, tx, sqlc.EventRecoveryListAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventRecoveryJobs{}, ErrNotFound
	}
	if err != nil {
		return api.EventRecoveryJobs{}, err
	}
	id := uuid.Nil.String()
	if cursor.ID != "" {
		id = cursor.ID
	}
	rows, err := q.EventRecoveryList(ctx, tx, sqlc.EventRecoveryListParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), FilterState: query.State, Mode: query.Mode, SubscriptionID: query.SubscriptionID, CreatedAfter: recoveryListTime(query.CreatedAfter), CreatedBefore: recoveryListTime(query.CreatedBefore), HasCursor: query.Cursor != "", CursorCreated: pgtypeFromTime(cursor.Created), CursorID: mustPgUUID(id), PageLimit: int32(query.Limit + 1)})
	if err != nil {
		return api.EventRecoveryJobs{}, err
	}
	jobs := make([]api.EventRecoveryJob, 0, len(rows))
	for _, row := range rows {
		job, err := eventRecoveryMetadata(sqlc.EventRecoveryGetRow(row))
		if err != nil {
			return api.EventRecoveryJobs{}, err
		}
		jobs = append(jobs, job)
	}
	out := recoveryListPage(jobs, query.Limit, cursor)
	return out, tx.Commit(ctx)
}
func (m *MemStore) ListEventRecoveries(ctx context.Context, account, app string, query api.EventRecoveryListQuery) (api.EventRecoveryJobs, error) {
	query, cursor, err := normalizeRecoveryList(account, app, query)
	if err != nil {
		return api.EventRecoveryJobs{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventRecoveryAppLocked(account, app) {
		return api.EventRecoveryJobs{}, ErrNotFound
	}
	jobs := []api.EventRecoveryJob{}
	for _, entry := range m.eventRecoveryJobs {
		if err := ctx.Err(); err != nil {
			return api.EventRecoveryJobs{}, err
		}
		if !sameMemUUID(entry.AccountID, account) || !sameMemUUID(entry.Job.AppID, app) {
			continue
		}
		copy := *entry
		job := memEventRecoveryResponse(&copy)
		job.Execution = nil
		mode := job.Selection.Mode
		if mode == "" {
			mode = "routing"
		}
		if query.State != "" && job.State != query.State || query.Mode != "" && mode != query.Mode {
			continue
		}
		if query.CreatedAfter != nil && !job.CreatedAt.After(*query.CreatedAfter) || query.CreatedBefore != nil && !job.CreatedAt.Before(*query.CreatedBefore) {
			continue
		}
		if query.Cursor != "" && (job.CreatedAt.After(cursor.Created) || job.CreatedAt.Equal(cursor.Created) && job.ID >= cursor.ID) {
			continue
		}
		if query.SubscriptionID != "" && job.Selection.SubscriptionID != query.SubscriptionID {
			match := false
			for _, item := range entry.Items {
				if item.SubscriptionID == query.SubscriptionID {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID > jobs[j].ID
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return recoveryListPage(jobs, query.Limit, cursor), nil
}
