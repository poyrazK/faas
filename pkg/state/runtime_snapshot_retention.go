package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RuntimeSnapshotRetentionReader keeps catalogued runtime captures out of
// application-deployment GC. Retirement does not grant artifact deletion:
// a separate release-retention decision is required before removing them.
type RuntimeSnapshotRetentionReader interface {
	RuntimeSnapshotRecords(context.Context) ([]RuntimeSnapshotRecord, error)
}

func (s *PgStore) RuntimeSnapshotRecords(ctx context.Context) ([]RuntimeSnapshotRecord, error) {
	rows, err := sqlc.New().RuntimeSnapshotRecords(ctx, s.pool)
	if err != nil {
		return nil, mapErr(err)
	}
	result := make([]RuntimeSnapshotRecord, 0, len(rows))
	for _, row := range rows {
		record := runtimeSnapshotFromSQL(row)
		if err := record.Validate(); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func (m *MemStore) RuntimeSnapshotRecords(ctx context.Context) ([]RuntimeSnapshotRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]RuntimeSnapshotRecord, 0, len(m.runtimeSnapshots))
	for _, record := range m.runtimeSnapshots {
		rows = append(rows, cloneRuntimeSnapshotRecord(record))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CatalogKey < rows[j].CatalogKey })
	return rows, nil
}
