package sched

import (
	"context"
	"reflect"
	"sync"

	"github.com/onebox-faas/faas/pkg/state"
)

// appLister is the slice of state.Store schedd's app pollers read.
type appLister interface {
	ListAllApps(ctx context.Context) ([]state.App, error)
	ListAppsByNodeID(ctx context.Context, nodeID string) ([]state.App, error)
}

// AppSnapshot serves the app lists schedd's pollers read every second (the
// scale-up, targets and floor triggers, the reaper and the scaling-policy
// observer). Each call reads the list's version, one row, and rereads the
// full rows only when it changed: on production-us the pollers listed every
// app with its manifest about 130 times a minute per schedd, while app rows
// change a few dozen times an hour. A list is never older than the call that
// returns it, so the pollers see what a direct read would. Callers get their
// own deep copy; a store without state.AppsListVersionReader is read directly.
type AppSnapshot struct {
	store   appLister
	version state.AppsListVersionReader

	mu      sync.Mutex
	entries map[string]*appSnapshotEntry
}

type appSnapshotEntry struct {
	mu      sync.Mutex
	valid   bool
	version state.AppsListVersion
	apps    []state.App
}

// NewAppSnapshot returns a snapshot of store's app lists.
func NewAppSnapshot(store appLister) *AppSnapshot {
	s := &AppSnapshot{store: store, entries: map[string]*appSnapshotEntry{}}
	s.version, _ = store.(state.AppsListVersionReader)
	return s
}

// ListAllApps returns every app that is not deleted, as the store does.
func (s *AppSnapshot) ListAllApps(ctx context.Context) ([]state.App, error) {
	return s.list(ctx, "")
}

// ListAppsByNodeID returns the apps nodeID owns, as the store does.
func (s *AppSnapshot) ListAppsByNodeID(ctx context.Context, nodeID string) ([]state.App, error) {
	return s.list(ctx, nodeID)
}

func (s *AppSnapshot) read(ctx context.Context, nodeID string) ([]state.App, error) {
	if nodeID == "" {
		return s.store.ListAllApps(ctx)
	}
	return s.store.ListAppsByNodeID(ctx, nodeID)
}

func (s *AppSnapshot) list(ctx context.Context, nodeID string) ([]state.App, error) {
	if s.version == nil {
		return s.read(ctx, nodeID)
	}
	s.mu.Lock()
	entry := s.entries[nodeID]
	if entry == nil {
		entry = &appSnapshotEntry{}
		s.entries[nodeID] = entry
	}
	s.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	// The version is read before the rows: a change committed between the
	// two reads leaves the stored version behind the rows, so the next call
	// reads them again instead of keeping rows older than their version.
	version, err := s.version.AppsListVersion(ctx, nodeID)
	if err != nil {
		entry.valid = false
		return s.read(ctx, nodeID)
	}
	if !entry.valid || version != entry.version {
		apps, err := s.read(ctx, nodeID)
		if err != nil {
			entry.valid = false
			return nil, err
		}
		entry.apps, entry.version, entry.valid = apps, version, true
	}
	return cloneApps(entry.apps), nil
}

// cloneApps copies apps so that no caller shares mutable memory with the
// snapshot or another caller: the reaper filters its list in place, and any
// caller may write through a pointer, slice or map field.
func cloneApps(apps []state.App) []state.App {
	if apps == nil {
		return nil
	}
	out := make([]state.App, len(apps))
	copy(out, apps)
	for i := range out {
		deepCopyInPlace(reflect.ValueOf(&out[i]).Elem())
	}
	return out
}

// deepCopyInPlace replaces every pointer, slice and map reachable through v's
// exported fields with a copy. Unexported fields stay shallow: in an App they
// belong to immutable values such as time.Time and netip.Addr.
func deepCopyInPlace(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !v.CanSet() {
			return
		}
		c := reflect.New(v.Elem().Type())
		c.Elem().Set(v.Elem())
		deepCopyInPlace(c.Elem())
		v.Set(c)
	case reflect.Slice:
		if v.IsNil() || !v.CanSet() {
			return
		}
		c := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(c, v)
		for i := range c.Len() {
			deepCopyInPlace(c.Index(i))
		}
		v.Set(c)
	case reflect.Map:
		if v.IsNil() || !v.CanSet() {
			return
		}
		c := reflect.MakeMapWithSize(v.Type(), v.Len())
		for it := v.MapRange(); it.Next(); {
			value := reflect.New(it.Value().Type()).Elem()
			value.Set(it.Value())
			deepCopyInPlace(value)
			c.SetMapIndex(it.Key(), value)
		}
		v.Set(c)
	case reflect.Interface:
		if v.IsNil() || !v.CanSet() {
			return
		}
		value := reflect.New(v.Elem().Type()).Elem()
		value.Set(v.Elem())
		deepCopyInPlace(value)
		v.Set(value)
	case reflect.Struct:
		for i := range v.NumField() {
			if field := v.Field(i); field.CanSet() {
				deepCopyInPlace(field)
			}
		}
	case reflect.Array:
		for i := range v.Len() {
			deepCopyInPlace(v.Index(i))
		}
	}
}
