package state

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

var _ ExclusiveWorkStore = (*MemStore)(nil)

func (s *MemStore) UpsertExclusiveWorkPolicy(ctx context.Context, a string, p exclusivework.Policy) (ExclusiveWorkPolicy, error) {
	return upsertExclusivePolicy(ctx, s.exclusiveAtomic, a, p)
}
func (s *MemStore) AdmitExclusiveOperation(ctx context.Context, a ExclusiveAdmission) (ExclusiveOperation, bool, error) {
	return admitExclusive(ctx, s.exclusiveAtomic, a)
}
func (s *MemStore) ClaimExclusiveOperation(ctx context.Context, a, id, inc string) (exclusivework.Claim, error) {
	return claimExclusive(ctx, s.exclusiveAtomic, a, id, inc)
}
func (s *MemStore) RenewExclusiveOperation(ctx context.Context, c exclusivework.Claim) (exclusivework.Claim, error) {
	return renewExclusive(ctx, s.exclusiveAtomic, c)
}
func (s *MemStore) CommitExclusiveOperation(ctx context.Context, c exclusivework.Claim, r json.RawMessage, e []exclusivework.Effect) error {
	return commitExclusive(ctx, s.exclusiveAtomic, c, r, e)
}
func (s *MemStore) RetryExclusiveOperation(ctx context.Context, c exclusivework.Claim, r string) error {
	return retryExclusive(ctx, s.exclusiveAtomic, c, r)
}
func (s *MemStore) CancelExclusiveOperation(ctx context.Context, a, id string) error {
	return cancelExclusive(ctx, s.exclusiveAtomic, a, id)
}
func (s *MemStore) ListExclusiveWorkPolicies(ctx context.Context, a string) (out []ExclusiveWorkPolicy, err error) {
	err = s.exclusiveAtomic(ctx, func(tx exclusiveTransaction) error { var e error; out, e = tx.policies(a); return e })
	return
}
func (s *MemStore) ExclusiveOperationByID(ctx context.Context, a, id string) (out ExclusiveOperation, err error) {
	err = s.exclusiveAtomic(ctx, func(tx exclusiveTransaction) error { var e error; out, e = tx.operation(a, id); return e })
	return
}

func (s *MemStore) ValidateExclusiveOperation(ctx context.Context, c exclusivework.Claim) (ExclusiveOperation, error) {
	return validateExclusive(ctx, s.exclusiveAtomic, c)
}
func (s *MemStore) FailExclusiveOperation(ctx context.Context, c exclusivework.Claim, reason string) error {
	return finishExclusiveFailure(ctx, s.exclusiveAtomic, c, reason, false)
}
func (s *MemStore) FailPendingExclusiveOperation(ctx context.Context, account, id, reason string) error {
	return failPendingExclusive(ctx, s.exclusiveAtomic, account, id, reason)
}
func (s *MemStore) DeferPendingExclusiveOperation(ctx context.Context, account, id, reason string) error {
	return deferPendingExclusive(ctx, s.exclusiveAtomic, account, id, reason)
}
