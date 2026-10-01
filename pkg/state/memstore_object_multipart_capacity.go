package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectMultipartCapacityStore = (*MemStore)(nil)

func (m *MemStore) AdmitObjectMultipartPart(_ context.Context, account, bucket, id string, part int32, size, maxObject int64, p api.ObjectStoragePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if !validMultipartCapacityUpload(m.objectMultipartUploads[id], account, bucket, false, now) || part < 1 || part > api.MaxMultipartParts || size < 1 || size > api.MaxObjectSinglePutBytes || maxObject < 1 || maxObject > api.MaxObjectUploadBytes {
		return ErrConflict
	}
	old := m.objectMultipartPartGrants[id][part]
	delta := max(int64(0), size-old)
	var total int64
	for _, v := range m.objectMultipartPartGrants[id] {
		total = boundedObjectAdd(total, v)
	}
	if total > maxObject-delta {
		return ErrObjectCapacity
	}
	if _, _, err := checkObjectAdmission(m.objectUsageLocked(account, now), bucket, delta, 0, true, true, p, now); err != nil {
		return err
	}
	if m.objectMultipartPartGrants == nil {
		m.objectMultipartPartGrants = map[string]map[int32]int64{}
	}
	if m.objectMultipartPartGrants[id] == nil {
		m.objectMultipartPartGrants[id] = map[int32]int64{}
	}
	m.objectMultipartPartGrants[id][part] = max(old, size)
	m.authorizeMultipartLocked(account, now)
	return nil
}

func (m *MemStore) authorizeMultipartLocked(account string, now time.Time) {
	if m.objectAuthorizations == nil {
		m.objectAuthorizations = map[string]int64{}
	}
	m.objectAuthorizations[account+ObjectStoragePeriod(now).String()]++
}

func (m *MemStore) AdmitObjectMultipartCompletion(_ context.Context, account, bucket, id, key string, size int64, p api.ObjectStoragePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	upload := m.objectMultipartUploads[id]
	if !validMultipartCapacityUpload(upload, account, bucket, true, now) || upload.Key != key || size < 1 || size > api.MaxObjectUploadBytes {
		return ErrConflict
	}
	var reserved int64
	for _, v := range m.objectMultipartPartGrants[id] {
		reserved = boundedObjectAdd(reserved, v)
	}
	snapshot := withoutMultipartReservation(m.objectUsageLocked(account, now), bucket, reserved)
	hash := objectKeyHash(key)
	old, exists := m.objectGrants[bucket][hash]
	delta, keys, err := checkObjectAdmission(snapshot, bucket, size, old, exists, true, p, now)
	if err != nil {
		return err
	}
	if m.objectGrants == nil {
		m.objectGrants = map[string]map[string]int64{}
	}
	if m.objectGrants[bucket] == nil {
		m.objectGrants[bucket] = map[string]int64{}
	}
	m.objectGrants[bucket][hash] = max(old, size)
	u := m.objectUsage[bucket]
	u.GrantedBytes += delta
	u.GrantedKeys += keys
	m.objectUsage[bucket] = u
	m.authorizeMultipartLocked(account, now)
	return nil
}

func (m *MemStore) ListObjectS3MultipartUploads(_ context.Context, account, app, bucket, prefix, keyMarker, uploadMarker string, limit int32) ([]ObjectMultipartUpload, error) {
	if limit < 1 || limit > api.MaxObjectS3ListItems+1 {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ObjectMultipartUpload{}
	for _, u := range m.objectMultipartUploads {
		if u.AccountID != account || u.AppID != app || u.BucketID != bucket || u.PartCount != 0 || u.State == ObjectMultipartInitiating || !objectMultipartLive(u.State) || !strings.HasPrefix(u.Key, prefix) {
			continue
		}
		if keyMarker != "" && (u.Key < keyMarker || u.Key == keyMarker && (uploadMarker == "" || u.ID <= uploadMarker)) {
			continue
		}
		u.Parts = cloneMultipartParts(u.Parts)
		u.Metadata = cloneObjectMultipartMetadata(u.Metadata)
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key || out[i].Key == out[j].Key && out[i].ID < out[j].ID
	})
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}
