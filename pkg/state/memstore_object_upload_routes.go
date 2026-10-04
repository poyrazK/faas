package state

import (
	"context"
	"slices"
	"sort"
	"time"
)

var _ ObjectUploadRouteStore = (*MemStore)(nil)

func cloneObjectUploadRoute(route ObjectUploadRoute) ObjectUploadRoute {
	route.AllowedContentTypes = slices.Clone(route.AllowedContentTypes)
	route.Encryption = route.Encryption.Clone()
	return route
}

func (m *MemStore) ListObjectUploadRoutes(_ context.Context, accountID, appID string) ([]ObjectUploadRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ObjectUploadRoute, 0)
	for _, route := range m.objectUploadRoutes {
		if route.AccountID == accountID && route.AppID == appID {
			out = append(out, cloneObjectUploadRoute(route))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) GetObjectUploadRoute(_ context.Context, accountID, appID, name string) (ObjectUploadRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, route := range m.objectUploadRoutes {
		if route.AccountID == accountID && route.AppID == appID && route.Name == name {
			return cloneObjectUploadRoute(route), nil
		}
	}
	return ObjectUploadRoute{}, ErrNotFound
}

func (m *MemStore) UpsertObjectUploadRoute(_ context.Context, route ObjectUploadRoute) (ObjectUploadRoute, error) {
	if route.ID == "" || route.AccountID == "" || route.AppID == "" || route.Name == "" || route.BucketID == "" || route.MaxBytes <= 0 || !route.Encryption.ValidFor(route.AccountID) {
		return ObjectUploadRoute{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !route.Encryption.Empty() {
		b := m.objectBuckets[route.BucketID]
		if b.AccountID != route.AccountID || b.AppID != route.AppID || b.State != "ready" {
			return ObjectUploadRoute{}, ErrNotFound
		}
	}
	now := time.Now().UTC()
	for id, existing := range m.objectUploadRoutes {
		if id != route.ID && existing.AppID == route.AppID && existing.Name == route.Name {
			return ObjectUploadRoute{}, ErrConflict
		}
	}
	if old, ok := m.objectUploadRoutes[route.ID]; ok {
		if old.AccountID != route.AccountID || old.AppID != route.AppID || old.Name != route.Name {
			return ObjectUploadRoute{}, ErrConflict
		}
		route.CreatedAt = old.CreatedAt
	} else if route.CreatedAt.IsZero() {
		route.CreatedAt = now
	}
	route.UpdatedAt = now
	m.objectUploadRoutes[route.ID] = cloneObjectUploadRoute(route)
	return cloneObjectUploadRoute(route), nil
}

func (m *MemStore) DeleteObjectUploadRoute(_ context.Context, accountID, appID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, route := range m.objectUploadRoutes {
		if route.AccountID == accountID && route.AppID == appID && route.Name == name {
			delete(m.objectUploadRoutes, id)
			for receiptID, receipt := range m.objectUploadCompletions {
				if receipt.RouteID == id {
					receipt.RouteID = ""
					m.objectUploadCompletions[receiptID] = receipt
				}
			}
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemStore) RecordObjectUploadCompletion(_ context.Context, completion ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if !emptyCopySourceProvenance(completion) || !completion.Encryption.Empty() || !completion.VerifiedEncryption.Empty() || completion.ID == "" || completion.RouteID == "" || completion.Key == "" || completion.Bytes < 0 {
		return ObjectUploadCompletion{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if (!m.objectUploadRoutes[completion.RouteID].Encryption.Empty() || m.objectBucketDefaultRequiresTrackingLocked(completion.BucketID)) && completion.Status != "rejected" {
		return ObjectUploadCompletion{}, ErrConflict
	}
	if _, exists := m.objectUploadCompletions[completion.ID]; exists {
		return ObjectUploadCompletion{}, ErrConflict
	}
	if completion.CreatedAt.IsZero() {
		completion.CreatedAt = time.Now().UTC()
	}
	m.objectUploadCompletions[completion.ID] = completion
	return cloneObjectUploadCompletion(completion), nil
}

func (m *MemStore) CreateObjectUploadIntent(_ context.Context, intent ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if !emptyCopySourceProvenance(intent) || !intent.Encryption.Empty() || !intent.VerifiedEncryption.Empty() || intent.ID == "" || intent.RouteID == "" || intent.Key == "" || intent.Bytes < 0 || intent.IdempotencyKey == "" || intent.RequestFingerprint == "" || intent.Status != "pending" {
		return ObjectUploadCompletion{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.objectUploadRoutes[intent.RouteID].Encryption.Empty() || m.objectBucketDefaultRequiresTrackingLocked(intent.BucketID) {
		return ObjectUploadCompletion{}, ErrConflict
	}
	if _, exists := m.objectUploadCompletions[intent.ID]; exists {
		return ObjectUploadCompletion{}, ErrConflict
	}
	for _, existing := range m.objectUploadCompletions {
		if existing.RouteID == intent.RouteID && existing.SubjectID == intent.SubjectID && existing.IdempotencyKey == intent.IdempotencyKey {
			return ObjectUploadCompletion{}, ErrConflict
		}
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now().UTC()
	}
	m.objectUploadCompletions[intent.ID] = intent
	return intent, nil
}

func (m *MemStore) GetObjectUploadIntent(_ context.Context, routeID, subjectID, idempotencyKey string) (ObjectUploadCompletion, error) {
	if routeID == "" || subjectID == "" || idempotencyKey == "" {
		return ObjectUploadCompletion{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, completion := range m.objectUploadCompletions {
		if completion.RouteID == routeID && completion.SubjectID == subjectID && completion.IdempotencyKey == idempotencyKey {
			return cloneObjectUploadCompletion(completion), nil
		}
	}
	return ObjectUploadCompletion{}, ErrNotFound
}

func (m *MemStore) UpdateObjectUploadCompletion(_ context.Context, completion ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if completion.ID == "" || completion.IdempotencyKey == "" || completion.Status == "pending" {
		return ObjectUploadCompletion{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.objectUploadCompletions[completion.ID]
	if !ok || existing.IdempotencyKey == "" || existing.WritePhase != "" && existing.WritePhase != "untracked" {
		return ObjectUploadCompletion{}, ErrNotFound
	}
	existing.ETag = completion.ETag
	existing.Status = completion.Status
	existing.ErrorCode = completion.ErrorCode
	existing.RequestID = completion.RequestID
	m.objectUploadCompletions[completion.ID] = existing
	return cloneObjectUploadCompletion(existing), nil
}
