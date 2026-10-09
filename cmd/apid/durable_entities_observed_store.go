// adr: 712
package main

import (
	"context"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/durableentity"
)

// Forward every optional capability, keeping metrics out of customer code.
// The callback follows WithOpsMetrics registry rebinding during server wiring.
type durableEntityObservedStore struct {
	durableentity.ObjectStore
	metrics func() *durableEntityMetrics
}

func (s durableEntityObservedStore) record(operation string, err error) {
	if m := s.metrics(); m != nil {
		outcome := durableEntityOutcome(err)
		if errors.Is(err, durableentity.ErrNotFound) {
			outcome = "missing"
		}
		m.operations.WithLabelValues(operation, outcome).Inc()
	}
}

func (s durableEntityObservedStore) Get(ctx context.Context, key string, maxBytes int64) ([]byte, string, error) {
	body, etag, err := s.ObjectStore.Get(ctx, key, maxBytes)
	s.record("get", err)
	return body, etag, err
}

func (s durableEntityObservedStore) Put(ctx context.Context, key string, body []byte, etag string) (string, error) {
	version, err := s.ObjectStore.Put(ctx, key, body, etag)
	if err == nil && version == "" {
		err = durableentity.ErrUncertain
	}
	if err != nil && !errors.Is(err, durableentity.ErrConflict) {
		err = errors.Join(durableentity.ErrUncertain, err)
	}
	s.record("put", err)
	if m := s.metrics(); m != nil && err == nil {
		m.uploadedBytes.WithLabelValues(durableEntityObjectKind(key)).Add(float64(len(body)))
	}
	return version, err
}

func durableEntityObjectKind(key string) string {
	switch {
	case strings.Contains(key, "/probes/"):
		return "probe"
	case strings.Contains(key, "/alarm-index/"):
		return "alarm_index"
	case strings.Contains(key, "/maintenance/") || strings.HasSuffix(key, "/maintenance.json") || strings.HasSuffix(key, "/inventory.json"):
		return "maintenance"
	case strings.HasSuffix(key, "/manifest.json"):
		return "manifest"
	case strings.Contains(key, "/snapshots/"):
		return "snapshot"
	case strings.Contains(key, "/receipts/"):
		return "receipt"
	default:
		return "other"
	}
}

func (s durableEntityObservedStore) ListEntityPrefixes(ctx context.Context, prefix, cursor string, limit int32) (durableentity.EntityPrefixPage, error) {
	lister, ok := s.ObjectStore.(durableentity.EntityPrefixLister)
	if !ok {
		return durableentity.EntityPrefixPage{}, durableentity.ErrUnsupported
	}
	page, err := lister.ListEntityPrefixes(ctx, prefix, cursor, limit)
	s.record("list_entities", err)
	return page, err
}

func (s durableEntityObservedStore) ListEntityObjects(ctx context.Context, prefix, cursor string, limit int32) (durableentity.CleanupObjects, error) {
	store, ok := s.ObjectStore.(durableentity.EntityObjectLister)
	if !ok {
		return durableentity.CleanupObjects{}, durableentity.ErrUnsupported
	}
	page, err := store.ListEntityObjects(ctx, prefix, cursor, limit)
	s.record("list_objects", err)
	return page, err
}

func (s durableEntityObservedStore) DeleteEntityObject(ctx context.Context, key string) error {
	store, ok := s.ObjectStore.(durableentity.CleanupStore)
	if !ok {
		return durableentity.ErrUnsupported
	}
	err := store.DeleteEntityObject(ctx, key)
	s.record("delete", err)
	return err
}
