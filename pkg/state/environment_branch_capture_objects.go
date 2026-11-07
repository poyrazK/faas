package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Capture objects (ADR-20261031193635 slice 5 follow-up). Every capture
// attempt writes its objects under a fresh key. schedd records the key before
// the snapshot; imaged deletes the objects of every key no ready capture
// references (failed and retried attempts, retired captures, deleted parent
// deployments) and then forgets it.

// EnvironmentBranchCaptureObject is one capture attempt's memory key; its
// vmstate, private drive and backing identity derive from it.
type EnvironmentBranchCaptureObject struct {
	StorageKey   string
	DeploymentID string
	CreatedAt    time.Time
}

// EnvironmentBranchCaptureObjectStore tracks capture objects until imaged
// deletes them.
type EnvironmentBranchCaptureObjectStore interface {
	// RecordEnvironmentBranchCaptureObject notes the leased attempt's memory
	// key before the snapshot is taken.
	RecordEnvironmentBranchCaptureObject(ctx context.Context, id, leaseToken, storageKey string) error
	// EnvironmentBranchCaptureGarbage lists keys recorded before `before`
	// that no ready capture references and no live attempt may still be
	// writing, oldest first.
	EnvironmentBranchCaptureGarbage(ctx context.Context, before time.Time, limit int) ([]EnvironmentBranchCaptureObject, error)
	// ForgetEnvironmentBranchCaptureObject drops a key whose objects are gone.
	// It refuses a key a ready capture references with ErrConflict.
	ForgetEnvironmentBranchCaptureObject(ctx context.Context, storageKey string) error
}

const environmentBranchCaptureObjectKeyMax = 512

func validEnvironmentBranchCaptureObjectKey(key string) bool {
	return len(key) <= environmentBranchCaptureObjectKeyMax && strings.HasPrefix(key, "snap/") && strings.HasSuffix(key, "/v2/mem") &&
		!strings.Contains(key, "..")
}

func clampEnvironmentBranchCaptureGarbageLimit(limit int) int {
	if limit <= 0 || limit > 500 {
		return 500
	}
	return limit
}

// MemStore

func (m *MemStore) RecordEnvironmentBranchCaptureObject(_ context.Context, id, leaseToken, storageKey string) error {
	if !validEnvironmentBranchCaptureObjectKey(storageKey) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	capture, err := m.leasedEnvironmentBranchCaptureLocked(id, leaseToken)
	if err != nil {
		return err
	}
	if m.environmentBranchCaptureObjects == nil {
		m.environmentBranchCaptureObjects = map[string]EnvironmentBranchCaptureObject{}
	}
	if _, ok := m.environmentBranchCaptureObjects[storageKey]; !ok {
		m.environmentBranchCaptureObjects[storageKey] = EnvironmentBranchCaptureObject{
			StorageKey: storageKey, DeploymentID: capture.DeploymentID, CreatedAt: m.clock().UTC()}
	}
	return nil
}

func (m *MemStore) EnvironmentBranchCaptureGarbage(_ context.Context, before time.Time, limit int) ([]EnvironmentBranchCaptureObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	var out []EnvironmentBranchCaptureObject
	for key, object := range m.environmentBranchCaptureObjects {
		if !object.CreatedAt.Before(before) || m.environmentBranchCaptureObjectReferencedLocked(key) {
			continue
		}
		if c, ok := m.environmentBranchCaptures[object.DeploymentID]; ok && c.State == EnvironmentBranchCaptureRequested &&
			c.LeaseToken != "" && c.leaseUntil.After(now) {
			continue
		}
		out = append(out, object)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].StorageKey < out[j].StorageKey
	})
	if limit = clampEnvironmentBranchCaptureGarbageLimit(limit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ForgetEnvironmentBranchCaptureObject(_ context.Context, storageKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.environmentBranchCaptureObjectReferencedLocked(storageKey) {
		return ErrConflict
	}
	delete(m.environmentBranchCaptureObjects, storageKey)
	return nil
}

func (m *MemStore) environmentBranchCaptureObjectReferencedLocked(storageKey string) bool {
	for _, c := range m.environmentBranchCaptures {
		if c.State == EnvironmentBranchCaptureReady && c.StorageKey == storageKey {
			return true
		}
	}
	return false
}

// PgStore

func (s *PgStore) RecordEnvironmentBranchCaptureObject(ctx context.Context, id, leaseToken, storageKey string) error {
	if !validEnvironmentBranchCaptureObjectKey(storageKey) {
		return ErrInvalidArgument
	}
	return s.finishEnvironmentBranchCapture(ctx, id, leaseToken, `
		insert into environment_branch_capture_objects (storage_key, deployment_id)
		select $3, deployment_id from environment_branch_captures
		 where id = $1 and lease_token = $2 and state = 'requested'
		on conflict (storage_key) do nothing`, storageKey)
}

func (s *PgStore) EnvironmentBranchCaptureGarbage(ctx context.Context, before time.Time, limit int) ([]EnvironmentBranchCaptureObject, error) {
	rows, err := s.pool.Query(ctx, `
		select o.storage_key, o.deployment_id::text, o.created_at
		  from environment_branch_capture_objects o
		 where o.created_at < $1
		   and not exists (select 1 from environment_branch_captures c
		                    where c.state = 'ready' and c.storage_key = o.storage_key)
		   and not exists (select 1 from environment_branch_captures c
		                    where c.deployment_id = o.deployment_id and c.state = 'requested' and c.lease_until > now())
		 order by o.created_at, o.storage_key
		 limit $2`, before.UTC(), clampEnvironmentBranchCaptureGarbageLimit(limit))
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (EnvironmentBranchCaptureObject, error) {
		var o EnvironmentBranchCaptureObject
		err := row.Scan(&o.StorageKey, &o.DeploymentID, &o.CreatedAt)
		o.CreatedAt = o.CreatedAt.UTC()
		return o, err
	})
	return out, mapErr(err)
}

func (s *PgStore) ForgetEnvironmentBranchCaptureObject(ctx context.Context, storageKey string) error {
	var referenced bool
	err := s.pool.QueryRow(ctx, `
		with forgotten as (
			delete from environment_branch_capture_objects o
			 where o.storage_key = $1
			   and not exists (select 1 from environment_branch_captures c
			                    where c.state = 'ready' and c.storage_key = o.storage_key)
		)
		select exists (select 1 from environment_branch_captures where state = 'ready' and storage_key = $1)`,
		storageKey).Scan(&referenced)
	if err != nil {
		return mapErr(err)
	}
	if referenced {
		return ErrConflict
	}
	return nil
}
