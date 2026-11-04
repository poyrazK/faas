package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cloneObjectCapacityCheck refuses a full clone whose source buckets would
// take the account past its stored bytes or keys (ADR-20261103160000). It
// counts each source bucket's conservative capacity, which bounds the
// versions the clone copies; the worker checks again with exact totals.
func (s *server) cloneObjectCapacityCheck(acct state.Account) func(context.Context, []string) error {
	return func(ctx context.Context, bucketIDs []string) error {
		return s.checkCloneObjectCapacity(ctx, acct, func(snapshot state.ObjectUsageSnapshot) (int64, int64, error) {
			bytes, keys, known := state.ObjectCloneSourceCapacity(snapshot, bucketIDs)
			if !known {
				return 0, 0, state.ErrObjectUsageStale
			}
			return bytes, keys, nil
		})
	}
}

// cloneObjectCapacityCheckBytes is the worker's check: the exact bytes and
// keys of the manifests it is about to copy.
func (s *server) cloneObjectCapacityCheckBytes(ctx context.Context, acct state.Account, bytes, keys int64) error {
	if s.cloneObjectCapacity != nil {
		return s.cloneObjectCapacity(ctx, acct, bytes, keys)
	}
	return s.checkCloneObjectCapacity(ctx, acct, func(state.ObjectUsageSnapshot) (int64, int64, error) { return bytes, keys, nil })
}

func (s *server) checkCloneObjectCapacity(ctx context.Context, acct state.Account, size func(state.ObjectUsageSnapshot) (int64, int64, error)) error {
	st, ok := s.store.(state.ObjectStorageAccountingStore)
	if !ok || s.objectStorage == nil {
		return state.ErrObjectUsageStale
	}
	now := time.Now().UTC()
	snapshot, err := st.ObjectUsage(ctx, acct.ResourceOwnerID(), now)
	if err != nil {
		return err
	}
	bytes, keys, err := size(snapshot)
	if err != nil {
		return err
	}
	return state.CheckObjectCloneCapacity(snapshot, s.objectStorage.Accounting, now, bytes, keys)
}

// cloneObjectCapacityProblem names the storage limit a clone's copy would
// pass, with the limit, the usage it would reach and the limits docs.
func cloneObjectCapacityProblem(err error) *api.Problem {
	problem := api.NewProblem(http.StatusConflict, "object_storage_capacity_reserved", "Object storage limit",
		"copying the source's buckets would take the account past its object storage limits")
	var limit *state.ObjectStorageLimitError
	if errors.As(err, &limit) {
		problem.Limit, problem.Observed = &limit.Limit, &limit.Observed
		problem.Detail += "; limit: " + limit.Kind
		problem.DocsURL = api.ObjectStorageLimitsDocsURL
	}
	return problem
}
