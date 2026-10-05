package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectWriteReceiptStore = (*MemStore)(nil)

func (m *MemStore) GetObjectWriteReceipt(_ context.Context, account, app, bucket, id string) (api.ObjectWriteReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectUploadCompletions[id]
	if !ok || c.AccountID != account || c.AppID != app || c.BucketID != bucket || !isTrackedWriteReceipt(c) {
		return api.ObjectWriteReceipt{}, ErrNotFound
	}
	return ViewObjectWriteReceipt(c), nil
}

func (m *MemStore) ListObjectWriteReceipts(_ context.Context, account, app, bucket, status string, limit int, cursor string) (api.ObjectWriteReceiptList, error) {
	status, limit, valid := api.ParseObjectWriteReceiptPage(status, limit, cursor)
	if !valid {
		return api.ObjectWriteReceiptList{}, ErrConflict
	}
	after, err := parseObjectWriteReceiptCursor(bucket, status, cursor)
	if err != nil {
		return api.ObjectWriteReceiptList{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app {
		return api.ObjectWriteReceiptList{}, ErrNotFound
	}
	rows := []ObjectUploadCompletion{}
	for _, c := range m.objectUploadCompletions {
		if c.AccountID != account || c.AppID != app || c.BucketID != bucket || !isTrackedWriteReceipt(c) || status != "all" && c.Status != status {
			continue
		}
		if cursor != "" && (c.CreatedAt.After(after.Created) || c.CreatedAt.Equal(after.Created) && c.ID >= after.ID) {
			continue
		}
		rows = append(rows, c)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.After(rows[j].CreatedAt) || rows[i].CreatedAt.Equal(rows[j].CreatedAt) && rows[i].ID > rows[j].ID
	})
	return objectWriteReceiptPage(rows, bucket, status, limit), nil
}
