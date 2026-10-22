package socket

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

const registryCleanupIndexPrefix = "tenant-registry/cleanup-index/v1/"
const RegistryCleanupReconcileInterval = time.Minute

type registryCleanupHint struct {
	Scope     string
	Epoch     uint64
	Operation string
}

func (h registryCleanupHint) key() string {
	return fmt.Sprintf("%s%s/%020d/%s", registryCleanupIndexPrefix, h.Scope, h.Epoch, h.Operation)
}
func parseRegistryCleanupHint(path string) (registryCleanupHint, bool) {
	if !strings.HasPrefix(path, registryCleanupIndexPrefix) {
		return registryCleanupHint{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, registryCleanupIndexPrefix), "/")
	if len(parts) != 3 || !validScopeKey(parts[0]) || len(parts[1]) != 20 || !sessionUUID(parts[2]) {
		return registryCleanupHint{}, false
	}
	epoch, err := strconv.ParseUint(parts[1], 10, 64)
	h := registryCleanupHint{parts[0], epoch, parts[2]}
	return h, err == nil && epoch > 0 && h.key() == path
}

// Keys are immutable, disposable hints, never ownership or deletion authority.
// Publication/removal failure must not change a known quota-root acknowledgement.
func (q *Quotas) syncRegistryCleanupHint(ctx context.Context, key string, c *RegistryCleanup) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	path := (registryCleanupHint{key, c.Epoch, c.OperationID}).key()
	if c.Phase == "complete" {
		store, ok := q.Store.(durableentity.CleanupStore)
		if !ok {
			return durableentity.ErrUnsupported
		}
		err := store.DeleteEntityObject(ctx, path)
		if errors.Is(err, durableentity.ErrNotFound) {
			return nil
		}
		return err
	}
	version, err := q.Store.Put(ctx, path, []byte(`{"hint":true}`), "")
	if errors.Is(err, durableentity.ErrConflict) {
		return nil
	} // Existing body grants no authority.
	if err != nil || version == "" {
		return errors.Join(durableentity.ErrUncertain, err)
	}
	return nil
}

// SweepIndexed reads only bounded hint candidates and their fresh authenticated
// roots. Future/unknown keys remain untouched. Root reconciliation independently
// repairs missing hints and provides fallback progress if index access fails.
func (q *Quotas) SweepIndexedRegistryCleanups(ctx context.Context, cursor string, allow func(durableentity.ID) bool) (RegistryCleanupWorkerPage, error) {
	out := RegistryCleanupWorkerPage{}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if len(cursor) > api.MaxObjectS3ListCursorBytes {
		return out, durableentity.ErrInvalid
	}
	store, ok := q.Store.(durableentity.CleanupStore)
	if !ok {
		return out, durableentity.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	scanCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	page, err := store.ListEntityObjects(scanCtx, registryCleanupIndexPrefix, cursor, registryWorkerPageSize)
	stop()
	if err != nil {
		return out, err
	}
	if len(page.Keys) > registryWorkerPageSize || !validRegistryCleanupPage(registryCleanupIndexPrefix, cursor, page) {
		return out, durableentity.ErrCorrupt
	}
	out.NextCursor = page.NextCursor
	out.Scanned = len(page.Keys)
	seen := map[string]bool{}
	for _, path := range page.Keys {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		hint, valid := parseRegistryCleanupHint(path)
		if !valid {
			continue
		}
		callCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		root, _, err := q.read(callCtx, hint.Scope)
		if err == nil && allow != nil && allow(registryCleanupScopeID(root)) {
			c := root.RegistryCleanup
			// A strictly older epoch is permanently obsolete. In the current
			// epoch, only the exact completed operation may be pruned. Never
			// prune future/mismatched hints based on a single root snapshot.
			if root.Registry != nil && (hint.Epoch < root.Registry.Epoch || c != nil && hint.Epoch == c.Epoch && hint.Operation == c.OperationID && c.Phase == "complete") {
				err = store.DeleteEntityObject(callCtx, path)
				if errors.Is(err, durableentity.ErrNotFound) {
					err = nil
				}
				if err == nil {
					out.Pruned++
				}
			} else if c != nil && hint.Epoch == c.Epoch && hint.Operation == c.OperationID && registryCleanupActive(root) && !seen[hint.Scope] {
				id := registryCleanupScopeID(root)
				if allow != nil && allow(id) {
					seen[hint.Scope] = true
					out.observeActive(c)
					if q.now().Before(c.NotBefore) {
						stop()
						continue
					}
					var attempted bool
					attempted, err = q.runRegistryCleanupAttempt(callCtx, hint.Scope, id, root)
					if attempted {
						out.Attempted++
					}
				}
			}
		}
		stop()
		if err != nil {
			out.Failed++
		}
	}
	return out, nil
}
func registryCleanupScopeID(r quotaRecord) durableentity.ID {
	return durableentity.ID{AccountID: r.Scope.Account, AppID: r.Scope.App, EnvironmentID: r.Scope.Environment, TenantID: r.Scope.Tenant, Namespace: "registry-cleanup", Key: "registry-cleanup"}
}

func (q *Quotas) CheckRegistryCleanupIndex(ctx context.Context) error {
	store, ok := q.Store.(durableentity.CleanupStore)
	if !ok {
		return durableentity.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityCleanupTimeout)
	defer cancel()
	page, err := store.ListEntityObjects(ctx, registryCleanupIndexPrefix, "", registryWorkerPageSize)
	if err != nil {
		return err
	}
	if len(page.Keys) > registryWorkerPageSize || !validRegistryCleanupPage(registryCleanupIndexPrefix, "", page) {
		return durableentity.ErrCorrupt
	}
	// Deliberately noncanonical: a probe can never be an executable hint.
	path := registryCleanupIndexPrefix + "probes/" + uuid.NewString() + "/probe"
	version, err := q.Store.Put(ctx, path, []byte(`{"probe":true}`), "")
	if err != nil || version == "" {
		return errors.Join(durableentity.ErrUncertain, err)
	}
	if err = store.DeleteEntityObject(ctx, path); err != nil {
		return err
	}
	if _, _, err = q.Store.Get(ctx, path, 4096); !errors.Is(err, durableentity.ErrNotFound) {
		return durableentity.ErrCorrupt
	}
	return nil
}
