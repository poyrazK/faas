package state

import (
	"bytes"
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"maps"
	"slices"
	"time"
)

type exclusiveMemoryTx struct {
	store            *MemStore
	policiesByName   map[string]ExclusiveWorkPolicy
	keys             map[string]exclusiveKey
	operations       map[string]ExclusiveOperation
	committedEffects map[string][]exclusivework.Effect
	submissions      map[string]string
	quotas           map[string]accountAsyncQuotaRow
}

// NewMemStoreWithExclusiveClock supplies an explicit clock for the memory
// ownership backend. PostgreSQL always uses its own clock after acquiring locks.
func NewMemStoreWithExclusiveClock(now func() time.Time) *MemStore {
	s := NewMemStore()
	s.exclusiveNow = now
	return s
}

func (s *MemStore) exclusiveAtomic(ctx context.Context, run func(exclusiveTransaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy-on-write gives the memory adapter transaction rollback semantics.
	tx := s.exclusiveMemoryTxLocked()
	if err := run(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.commitExclusiveMemoryTxLocked(tx)
	return nil
}

// exclusiveMemoryTxLocked and commitExclusiveMemoryTxLocked are also used by
// state transitions that atomically combine operation admission with another
// MemStore record while already holding s.mu.
func (s *MemStore) exclusiveMemoryTxLocked() *exclusiveMemoryTx {
	tx := &exclusiveMemoryTx{quotas: maps.Clone(s.accountAsyncQuota), submissions: maps.Clone(s.exclusiveSubmissions), store: s, policiesByName: maps.Clone(s.exclusivePolicies),
		keys: maps.Clone(s.exclusiveKeys), operations: maps.Clone(s.exclusiveOperations), committedEffects: maps.Clone(s.exclusiveEffects)}
	if tx.submissions == nil {
		tx.submissions = map[string]string{}
	}
	if tx.quotas == nil {
		tx.quotas = map[string]accountAsyncQuotaRow{}
	}
	if tx.policiesByName == nil {
		tx.policiesByName = map[string]ExclusiveWorkPolicy{}
	}
	if tx.keys == nil {
		tx.keys = map[string]exclusiveKey{}
	}
	if tx.operations == nil {
		tx.operations = map[string]ExclusiveOperation{}
	}
	if tx.committedEffects == nil {
		tx.committedEffects = map[string][]exclusivework.Effect{}
	}
	return tx
}

func (s *MemStore) commitExclusiveMemoryTxLocked(tx *exclusiveMemoryTx) {
	s.exclusiveSubmissions = tx.submissions
	s.accountAsyncQuota = tx.quotas
	s.exclusivePolicies, s.exclusiveKeys, s.exclusiveOperations, s.exclusiveEffects = tx.policiesByName, tx.keys, tx.operations, tx.committedEffects
}

func (tx *exclusiveMemoryTx) lockAccount(id string) error {
	a, ok := tx.store.accounts[id]
	if !ok || a.Status != AccountActive {
		return ErrNotFound
	}
	return nil
}

func (tx *exclusiveMemoryTx) policyInUse(policy ExclusiveWorkPolicy) (bool, error) {
	for _, operation := range tx.operations {
		if tx.keys[operation.KeyID].PolicyID == policy.ID && (operation.State == "pending" || operation.State == "running") {
			return true, nil
		}
	}
	for _, binding := range tx.store.exclusiveTriggerBindings {
		if canonicalMemUUID(binding.AccountID) == canonicalMemUUID(policy.AccountID) && binding.PolicyName == policy.Policy.Name {
			return true, nil
		}
	}
	return false, nil
}
func (tx *exclusiveMemoryTx) appScope(account, id string) (string, error) {
	a, ok := tx.store.apps[id]
	if !ok || a.AccountID != account || a.Status == AppDeleted {
		return "", ErrNotFound
	}
	return a.ProjectID, nil
}
func (tx *exclusiveMemoryTx) jobScope(account, id string) error {
	job, ok := tx.store.jobs[id]
	if !ok || job.AccountID != account || job.Status == "deleted" {
		return ErrNotFound
	}
	return nil
}
func (tx *exclusiveMemoryTx) jobRunScope(account, operationID string, generation int64) error {
	for _, run := range tx.store.jobRuns {
		if run.AccountID == account && run.ExclusiveOperationID == operationID && run.ExclusiveGeneration == generation {
			return nil
		}
	}
	return exclusivework.ErrStaleOwner
}
func (tx *exclusiveMemoryTx) appTaskScope(account, operationID string, generation int64) error {
	for _, task := range tx.store.appTasks {
		if task.AccountID == account && task.ExclusiveOperationID == operationID && task.ExclusiveGeneration == generation {
			return nil
		}
	}
	return exclusivework.ErrStaleOwner
}
func (tx *exclusiveMemoryTx) tenantScope(account, id string) error {
	t, ok := tx.store.platformTenants[id]
	if !ok || t.AccountID != account {
		return ErrNotFound
	}
	if t.Status != PlatformTenantActive {
		return ErrPlatformTenantSuspended
	}
	return nil
}
func (tx *exclusiveMemoryTx) environmentScope(account, project, id string) error {
	for _, e := range tx.store.projectEnvironments {
		if e.ID == id && e.AccountID == account && e.ProjectID == project {
			return nil
		}
	}
	return ErrNotFound
}
func (tx *exclusiveMemoryTx) now() (time.Time, error) {
	if tx.store.exclusiveNow != nil {
		return tx.store.exclusiveNow().UTC(), nil
	}
	return time.Now().UTC(), nil
}
func cloneExclusivePolicy(p ExclusiveWorkPolicy) ExclusiveWorkPolicy {
	p.Policy.MemberAppIDs = slices.Clone(p.Policy.MemberAppIDs)
	p.Policy.MemberJobIDs = slices.Clone(p.Policy.MemberJobIDs)
	return p
}
func cloneExclusiveOperation(op ExclusiveOperation) ExclusiveOperation {
	op.Policy.MemberAppIDs = slices.Clone(op.Policy.MemberAppIDs)
	op.Policy.MemberJobIDs = slices.Clone(op.Policy.MemberJobIDs)
	op.Request = slices.Clone(op.Request)
	op.Result = slices.Clone(op.Result)
	op.RequestDigest = slices.Clone(op.RequestDigest)
	op.EquivalenceDigest = slices.Clone(op.EquivalenceDigest)
	op.IdempotencyDigest = slices.Clone(op.IdempotencyDigest)
	if op.LeaseExpiresAt != nil {
		v := *op.LeaseExpiresAt
		op.LeaseExpiresAt = &v
	}
	if op.AttemptDeadline != nil {
		v := *op.AttemptDeadline
		op.AttemptDeadline = &v
	}
	if op.CompletedAt != nil {
		v := *op.CompletedAt
		op.CompletedAt = &v
	}
	return op
}
func (tx *exclusiveMemoryTx) policy(account, name string) (ExclusiveWorkPolicy, error) {
	p, ok := tx.policiesByName[account+"\x00"+name]
	if !ok {
		return p, ErrNotFound
	}
	return cloneExclusivePolicy(p), nil
}
func (tx *exclusiveMemoryTx) policies(account string) (out []ExclusiveWorkPolicy, err error) {
	out = []ExclusiveWorkPolicy{}
	for _, p := range tx.policiesByName {
		if p.AccountID == account {
			out = append(out, cloneExclusivePolicy(p))
		}
	}
	slices.SortFunc(out, func(a, b ExclusiveWorkPolicy) int {
		if a.Policy.Name < b.Policy.Name {
			return -1
		}
		if a.Policy.Name > b.Policy.Name {
			return 1
		}
		return 0
	})
	return
}
func (tx *exclusiveMemoryTx) savePolicy(p ExclusiveWorkPolicy) (ExclusiveWorkPolicy, error) {
	now, _ := tx.now()
	p.Revision++
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	tx.policiesByName[p.AccountID+"\x00"+p.Policy.Name] = cloneExclusivePolicy(p)
	return cloneExclusivePolicy(p), nil
}
func (tx *exclusiveMemoryTx) ensureKey(k exclusiveKey) (exclusiveKey, error) {
	for _, old := range tx.keys {
		if old.PolicyID == k.PolicyID && old.ScopeID == k.ScopeID && old.EnvironmentID == k.EnvironmentID && bytes.Equal(old.Digest, k.Digest) {
			return old, nil
		}
	}
	k.NextSequence = 1
	k.Digest = slices.Clone(k.Digest)
	tx.keys[k.ID] = k
	return k, nil
}
func (tx *exclusiveMemoryTx) key(account, id string) (exclusiveKey, error) {
	k, ok := tx.keys[id]
	if !ok || k.AccountID != account {
		return k, ErrNotFound
	}
	return k, nil
}
func (tx *exclusiveMemoryTx) saveKey(k exclusiveKey) error { tx.keys[k.ID] = k; return nil }
func (tx *exclusiveMemoryTx) operation(account, id string) (ExclusiveOperation, error) {
	o, ok := tx.operations[id]
	if !ok || o.AccountID != account {
		return ExclusiveOperation{}, ErrNotFound
	}
	return cloneExclusiveOperation(o), nil
}
func (tx *exclusiveMemoryTx) replay(key string, digest []byte) (ExclusiveOperation, error) {
	id, ok := tx.submissions[key+string(digest)]
	if !ok {
		return ExclusiveOperation{}, ErrNotFound
	}
	return cloneExclusiveOperation(tx.operations[id]), nil
}
func (tx *exclusiveMemoryTx) bindSubmission(key string, digest []byte, id string) error {
	if digest != nil {
		tx.submissions[key+string(digest)] = id
	}
	return nil
}
func (tx *exclusiveMemoryTx) active(key string) (out []ExclusiveOperation, err error) {
	out = []ExclusiveOperation{}
	for _, o := range tx.operations {
		if o.KeyID == key && (o.State == "pending" || o.State == "running") {
			out = append(out, cloneExclusiveOperation(o))
		}
	}
	slices.SortFunc(out, func(a, b ExclusiveOperation) int {
		if a.Sequence < b.Sequence {
			return -1
		}
		if a.Sequence > b.Sequence {
			return 1
		}
		return 0
	})
	return
}
func (tx *exclusiveMemoryTx) pendingCount(account string) (n int64, err error) {
	for _, o := range tx.operations {
		if o.AccountID == account && (o.State == "pending" || o.State == "running") {
			n++
		}
	}
	return
}
func (tx *exclusiveMemoryTx) insert(o ExclusiveOperation) (ExclusiveOperation, error) {
	o.CreatedAt, _ = tx.now()
	o.DueAt = o.CreatedAt
	tx.operations[o.ID] = cloneExclusiveOperation(o)
	return cloneExclusiveOperation(o), nil
}
func (tx *exclusiveMemoryTx) save(o ExclusiveOperation) error {
	if old := tx.operations[o.ID]; old.QuotaReserved && o.State != "running" {
		r := tx.quotas[o.AccountID]
		if r.CurrentInflight > 0 {
			r.CurrentInflight--
		}
		tx.quotas[o.AccountID], o.QuotaReserved = r, false
	}
	tx.operations[o.ID] = cloneExclusiveOperation(o)
	return nil
}

func (tx *exclusiveMemoryTx) reserve(account string) error {
	r := tx.quotas[account]
	r.MaxInflight = api.MustLimitsFor(tx.store.accounts[account].Plan).MaxAsyncInvocationsPerAccount
	if r.CurrentInflight >= r.MaxInflight {
		return ErrQuotaExceeded
	}
	r.CurrentInflight++
	tx.quotas[account] = r
	return nil
}
func (tx *exclusiveMemoryTx) effects(o ExclusiveOperation, effects []exclusivework.Effect) error {
	copyEffects := make([]exclusivework.Effect, len(effects))
	for i, e := range effects {
		copyEffects[i] = e
		copyEffects[i].Payload = slices.Clone(e.Payload)
	}
	tx.committedEffects[o.ID] = copyEffects
	return nil
}

func (tx *exclusiveMemoryTx) runtimeScope(account, app, id, wake, node string) error {
	i, ok := tx.store.instances[id]
	a := tx.store.apps[app]
	if !ok || tx.store.exclusiveCaptures[id] || i.AppID != app || a.AccountID != account || i.WakeID != wake || i.NodeID != node || i.State != string(StateRunning) {
		return exclusivework.ErrStaleOwner
	}
	return nil
}
