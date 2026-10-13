package socket

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

var ErrRegistryCleanupPending = errors.New("durable registry cleanup blocks this mutation")

type RegistryCleanup struct {
	OperationID    string    `json:"operation_id"`
	SourceRevision string    `json:"source_revision"`
	Actor          string    `json:"actor"`
	At             time.Time `json:"at"`
	Revision       uint64    `json:"revision"`
	Epoch          uint64    `json:"epoch"`
	Phase          string    `json:"phase"`
	NotBefore      time.Time `json:"not_before"`
	Cursor         string    `json:"cursor,omitempty"`
	DeletedObjects uint64    `json:"deleted_objects"`
}

func registryCleanupActive(r quotaRecord) bool {
	return r.RegistryCleanup != nil && r.RegistryCleanup.Phase != "complete"
}

// Schema 5 retains its original admission hold until operators complete it.
// Schema 6 fences older readers and permits current-epoch quota mutations.
func registryCleanupHoldsWrites(r quotaRecord) bool {
	return r.Schema < 6 && registryCleanupActive(r)
}
func registryEpochPrefix(scope string, epoch uint64) string {
	return "tenant-registry/v2/" + scope + "/" + fmt.Sprintf("%020d", epoch) + "/"
}
func validRegistryCleanup(r quotaRecord) bool {
	if r.Schema < 5 {
		return r.RegistryCleanup == nil && (r.Registry == nil || r.Registry.Epoch == 0)
	}
	c := r.RegistryCleanup
	if r.Registry == nil || r.Registry.Epoch == 0 || c == nil || c.Epoch != r.Registry.Epoch || !sessionUUID(c.OperationID) || !validScopeKey(c.SourceRevision) || !grantIdentity(c.Actor) || c.At.IsZero() || c.NotBefore.Before(c.At.Add(api.MaxDurableEntityLease+api.DurableEntityInvokeTimeout)) || c.Revision == 0 || len(c.Cursor) > api.MaxObjectS3ListCursorBytes {
		return false
	}
	switch c.Phase {
	case "legacy_cleanup", "legacy_verify", "epoch_cleanup", "epoch_verify":
		return true
	case "complete":
		return c.Cursor == ""
	default:
		return false
	}
}
func (q *Quotas) RegistryCleanupStatus(ctx context.Context, id durableentity.ID) (*RegistryCleanup, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return nil, err
	}
	r, _, err := q.read(ctx, scopeKey(scope))
	if err != nil {
		return nil, err
	}
	if r.RegistryCleanup == nil {
		return nil, nil
	}
	copy := *r.RegistryCleanup
	return &copy, nil
}

// Start rotates every live reference before publishing schema 6.
// Old writers' CAS versions are invalidated; their keys can never be live again.
func (q *Quotas) StartRegistryCleanup(ctx context.Context, id durableentity.ID, operation, revision, actor string) (*RegistryCleanup, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return nil, err
	}
	if !sessionUUID(operation) || !validScopeKey(revision) || !grantIdentity(actor) {
		return nil, durableentity.ErrInvalid
	}
	if _, ok := q.Store.(durableentity.CleanupStore); !ok {
		return nil, durableentity.ErrUnsupported
	}
	key := scopeKey(scope)
	r, v, err := q.read(ctx, key)
	if err != nil {
		return nil, err
	}
	if c := r.RegistryCleanup; c != nil && c.OperationID == operation {
		if c.SourceRevision != revision {
			return nil, durableentity.ErrRequestConflict
		}
		_ = q.syncRegistryCleanupHint(ctx, key, c)
		copy := *c
		return &copy, nil
	}
	if registryDigest([]byte(v)) != revision {
		return nil, durableentity.ErrConflict
	}
	if r.Schema < 4 || r.Limits != q.Limits || r.Migration == nil || r.Migration.Phase != "complete" || pendingRetirements(r) {
		return nil, durableentity.ErrInventoryPending
	}
	if registryCleanupActive(r) {
		return nil, ErrRegistryCleanupPending
	}
	prune(&r, q.now())
	if r.Registry.Epoch == math.MaxUint64 {
		return nil, durableentity.ErrLimit
	}
	root := *r.Registry
	root.Epoch++
	root.Shards = nil
	r.Registry = &root
	r.Schema = 6
	now := q.now()
	r.RegistryCleanup = &RegistryCleanup{OperationID: operation, SourceRevision: revision, Actor: actor, At: now, Revision: 1, Epoch: root.Epoch, Phase: "legacy_cleanup", NotBefore: now.Add(api.MaxDurableEntityLease + api.DurableEntityInvokeTimeout)}
	if err = q.commitRegistryCleanup(ctx, key, r, v); err != nil {
		return nil, err
	}
	_ = q.syncRegistryCleanupHint(ctx, key, r.RegistryCleanup)
	copy := *r.RegistryCleanup
	return &copy, nil
}
func (q *Quotas) commitRegistryCleanup(ctx context.Context, key string, r quotaRecord, version string) error {
	body, err := q.marshalQuota(ctx, key, r)
	if err != nil {
		return err
	}
	v, err := q.Store.Put(ctx, "tenant-quotas/v1/"+key, body, version)
	if errors.Is(err, durableentity.ErrConflict) {
		return err
	}
	if err != nil || v == "" {
		return errors.Join(durableentity.ErrUncertain, err)
	}
	return nil
}

// Recognize only canonical immutable keys, strictly older than the barrier.
// Unknown names, future epochs and the entire current epoch are retained.
func obsoleteRegistryKey(scope, key string, barrier uint64) bool {
	legacy := "tenant-registry/v1/" + scope + "/"
	suffix := ""
	if strings.HasPrefix(key, legacy) {
		suffix = strings.TrimPrefix(key, legacy)
	} else {
		prefix := "tenant-registry/v2/" + scope + "/"
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		parts := strings.Split(strings.TrimPrefix(key, prefix), "/")
		if len(parts) != 3 || len(parts[0]) != 20 {
			return false
		}
		epoch, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil || epoch == 0 || fmt.Sprintf("%020d", epoch) != parts[0] || epoch >= barrier {
			return false
		}
		suffix = parts[1] + "/" + parts[2]
	}
	parts := strings.Split(suffix, "/")
	return len(parts) == 2 && len(parts[0]) == 1 && strings.Contains("0123456789abcdef", parts[0]) && validScopeKey(parts[1])
}
func validRegistryCleanupPage(prefix, cursor string, page durableentity.CleanupObjects) bool {
	if len(page.Keys) > api.DurableEntityCleanupPageSize || len(page.NextCursor) > api.MaxObjectS3ListCursorBytes || page.NextCursor != "" && (page.NextCursor == cursor || len(page.Keys) == 0) {
		return false
	}
	seen := map[string]bool{}
	for _, key := range page.Keys {
		if !strings.HasPrefix(key, prefix) || len(key) > 1024 || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
func (q *Quotas) StepRegistryCleanup(ctx context.Context, id durableentity.ID, operation string, revision uint64, restart bool) (*RegistryCleanup, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return nil, err
	}
	if !sessionUUID(operation) || revision == 0 {
		return nil, durableentity.ErrInvalid
	}
	store, ok := q.Store.(durableentity.CleanupStore)
	if !ok {
		return nil, durableentity.ErrUnsupported
	}
	key := scopeKey(scope)
	r, v, err := q.read(ctx, key)
	if err != nil {
		return nil, err
	}
	c := r.RegistryCleanup
	if c == nil || c.OperationID != operation || c.Revision != revision {
		return nil, durableentity.ErrConflict
	}
	original := *c
	if c.Phase == "complete" {
		_ = q.syncRegistryCleanupHint(ctx, key, c)
		copy := *c
		return &copy, nil
	}
	if q.now().Before(c.NotBefore) {
		return nil, durableentity.ErrBusy
	}
	if c.Revision == math.MaxUint64 {
		return nil, durableentity.ErrLimit
	}
	if restart {
		c.Phase = "legacy_cleanup"
		c.Cursor = ""
	} else {
		legacy := strings.HasPrefix(c.Phase, "legacy_")
		prefix := "tenant-registry/v2/" + key + "/"
		if legacy {
			prefix = "tenant-registry/v1/" + key + "/"
		}
		page, err := store.ListEntityObjects(ctx, prefix, c.Cursor, api.DurableEntityCleanupPageSize)
		if err != nil {
			return nil, err
		}
		if !validRegistryCleanupPage(prefix, c.Cursor, page) {
			return nil, durableentity.ErrCorrupt
		}
		verify := strings.HasSuffix(c.Phase, "_verify")
		found := false
		for _, object := range page.Keys {
			if !obsoleteRegistryKey(key, object, c.Epoch) {
				continue
			}
			found = true
			if verify {
				continue
			}
			if c.DeletedObjects == math.MaxUint64 {
				return nil, durableentity.ErrLimit
			}
			if err = store.DeleteEntityObject(ctx, object); err != nil && !errors.Is(err, durableentity.ErrNotFound) {
				return nil, errors.Join(durableentity.ErrUncertain, err)
			}
			c.DeletedObjects++
		}
		c.Cursor = page.NextCursor
		if verify && found {
			if legacy {
				c.Phase = "legacy_cleanup"
			} else {
				c.Phase = "epoch_cleanup"
			}
			c.Cursor = ""
		} else if c.Cursor == "" {
			switch c.Phase {
			case "legacy_cleanup":
				c.Phase = "legacy_verify"
			case "legacy_verify":
				c.Phase = "epoch_cleanup"
			case "epoch_cleanup":
				c.Phase = "epoch_verify"
			case "epoch_verify":
				c.Phase = "complete"
			}
		}
	}
	c.Revision++
	if err = q.commitRegistryCleanupCheckpoint(ctx, key, r, v, original); err != nil {
		return nil, err
	}
	if c.Phase == "complete" {
		_ = q.syncRegistryCleanupHint(ctx, key, c)
	}
	copy := *c
	return &copy, nil
}

// A page is listed/deleted once. On definite root conflicts, merge only its
// checkpoint into fresh quota state; never overwrite concurrent reservations,
// counters, grants, connections or retirement credit. An advanced checkpoint
// fences this worker, and unknown acknowledgements require status inspection.
func (q *Quotas) commitRegistryCleanupCheckpoint(ctx context.Context, key string, r quotaRecord, version string, original RegistryCleanup) error {
	next := *r.RegistryCleanup
	for attempt := 0; attempt < 8; attempt++ {
		err := q.commitRegistryCleanup(ctx, key, r, version)
		if !errors.Is(err, durableentity.ErrConflict) {
			return err
		}
		fresh, freshVersion, err := q.read(ctx, key)
		if err != nil {
			return err
		}
		if fresh.RegistryCleanup == nil || *fresh.RegistryCleanup != original || fresh.Registry.Epoch != original.Epoch || fresh.Schema != r.Schema {
			return durableentity.ErrConflict
		}
		fresh.RegistryCleanup = &next
		r, version = fresh, freshVersion
	}
	return durableentity.ErrBusy
}

// Probe private listing/deletion using only this process's unique probe key.
func (q *Quotas) CheckRegistryCleanup(ctx context.Context) error {
	store, ok := q.Store.(durableentity.CleanupStore)
	if !ok {
		return durableentity.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityCleanupTimeout)
	defer cancel()
	prefix := "tenant-registry/probes/" + uuid.NewString() + "/"
	key := prefix + "probe.json"
	version, err := q.Store.Put(ctx, key, []byte(`{"probe":true}`), "")
	if err != nil || version == "" {
		return errors.Join(durableentity.ErrUncertain, err)
	}
	page, err := store.ListEntityObjects(ctx, prefix, "", api.DurableEntityCleanupPageSize)
	if err != nil {
		return err
	}
	if len(page.Keys) != 1 || page.Keys[0] != key || page.NextCursor != "" {
		return durableentity.ErrUnsupported
	}
	if err = store.DeleteEntityObject(ctx, key); err != nil {
		return err
	}
	if _, _, err = q.Store.Get(ctx, key, 1024); !errors.Is(err, durableentity.ErrNotFound) {
		return errors.Join(durableentity.ErrUnsupported, err)
	}
	return nil
}
