package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) ListAccountObjectBuckets(ctx context.Context, account string, limit int32) ([]ObjectBucket, error) {
	if limit < 1 || limit > api.ObjectOwnedCleanupBucketBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		var zero0 []ObjectBucket
		return zero0, err
	}

	rows := []ObjectBucket{}
	for _, b := range m.objectBuckets {
		if b.AccountID == account && b.State != "deleted" && m.personalObjectBucketLocked(b) {
			rows = append(rows, b)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	if len(rows) > int(limit) {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *PgStore) ListAccountObjectBuckets(ctx context.Context, account string, limit int32) ([]ObjectBucket, error) {
	if limit < 1 || limit > api.ObjectOwnedCleanupBucketBatch {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().ObjectAccountBucketCleanupList(ctx, s.pool, sqlc.ObjectAccountBucketCleanupListParams{AccountID: mustPgUUID(account), Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]ObjectBucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, objectBucketFromSQL(row))
	}
	return out, nil
}
