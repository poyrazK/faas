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
	if route.ID == "" || route.AccountID == "" || route.AppID == "" || route.Name == "" || route.BucketID == "" || route.MaxBytes <= 0 {
		return ObjectUploadRoute{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for id, existing := range m.objectUploadRoutes {
		if id != route.ID && existing.AppID == route.AppID && existing.Name == route.Name {
			return ObjectUploadRoute{}, ErrConflict
		}
	}
	if old, ok := m.objectUploadRoutes[route.ID]; ok {
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
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemStore) RecordObjectUploadCompletion(_ context.Context, completion ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if completion.ID == "" || completion.RouteID == "" || completion.Key == "" || completion.Bytes < 0 {
		return ObjectUploadCompletion{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if completion.CreatedAt.IsZero() {
		completion.CreatedAt = time.Now().UTC()
	}
	m.objectUploadCompletions[completion.ID] = completion
	return completion, nil
}
