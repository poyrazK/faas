package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

var (
	ErrKeyedReplayNotAllowed = errors.New("state: keyed replay requires failed unbound policy work")
	ErrKeyedReplayExpired    = errors.New("state: keyed replay pending deadline expired")
)

// Only transport trace/version headers and execution/result lifetimes are
// refreshed by the API. Work identity, payload and lineage come from storage.
type KeyedInvocationReplayOptions struct {
	Headers              json.RawMessage
	DeadlineAt           *time.Time
	ResultRetentionUntil *time.Time
}

type KeyedInvocationReplayStore interface {
	ExistingKeyedInvocationReplay(context.Context, string, string) (Invocation, error)
	ReplayKeyedInvocation(context.Context, string, string, KeyedInvocationReplayOptions) (Invocation, error)
}

// ExistingKeyedInvocationReplay lets retries read their durable response before
// validating deployment pins which may have expired since initial admission.
func (s *PgStore) ExistingKeyedInvocationReplay(ctx context.Context, accountID, parentID string) (Invocation, error) {
	account, err := uuid.Parse(accountID)
	if err != nil {
		return Invocation{}, ErrNotFound
	}
	id, err := uuid.Parse(parentID)
	if err != nil {
		return Invocation{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invocation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.KeyedReplayParent(ctx, tx, sqlc.KeyedReplayParentParams{ID: mustPgUUID(id.String()), AccountID: mustPgUUID(account.String())})
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	parent, err := invocationFromSQL(row)
	if err != nil {
		return Invocation{}, err
	}
	if !keyedReplayAllowed(parent) {
		return Invocation{}, ErrKeyedReplayNotAllowed
	}
	childID, err := q.KeyedReplayChildID(ctx, tx, row.ID)
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	child, err := q.KeyedReplayChild(ctx, tx, childID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, ErrConflict
	}
	if err != nil {
		return Invocation{}, err
	}
	return ownedKeyedReplayChild(parent, child)
}

func (m *MemStore) ExistingKeyedInvocationReplay(_ context.Context, accountID, parentID string) (Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parent, ok := m.invocations[parentID]
	if !ok || !sameMemUUID(parent.AccountID, accountID) || !sameMemUUID(m.apps[parent.AppID].AccountID, accountID) {
		return Invocation{}, ErrNotFound
	}
	if _, _, linked := m.operationForInvocationLocked(parent.ID); linked {
		parent.OperationID = "owned"
	}
	if !keyedReplayAllowed(parent) {
		return Invocation{}, ErrKeyedReplayNotAllowed
	}
	childID := m.keyedReplayChildren[parentID]
	if childID == "" {
		return Invocation{}, ErrNotFound
	}
	child, ok := m.invocations[childID]
	if !ok {
		return Invocation{}, ErrConflict
	}
	if !invocationReplayChildMatches(parent, child) {
		return Invocation{}, ErrConflict
	}
	return cloneInvocationReplay(child), nil
}

func keyedReplayAllowed(parent Invocation) bool {
	return parent.State == InvocationFailed && parent.WorkPolicyName != "" &&
		len(parent.WorkKeyDigest) == 32 && parent.WorkSequence > 0 &&
		parent.QueueBindingID == "" && parent.QueueName == "" && parent.EnvironmentID == "" && !InvocationHasOperation(parent)
}

func newKeyedReplay(parent Invocation, opts KeyedInvocationReplayOptions, sequence int64, now time.Time) Invocation {
	inv := Invocation{
		ID: uuid.NewString(), AppID: parent.AppID, AccountID: parent.AccountID,
		DeploymentScope: parent.DeploymentScope, PlatformTenantID: parent.PlatformTenantID,
		Source: InvocationReplay, State: InvocationPending, Method: parent.Method, Path: parent.Path,
		Payload: bytes.Clone(parent.Payload), Headers: bytes.Clone(parent.Headers), DueAt: now, CreatedAt: now,
		WorkPolicyName: parent.WorkPolicyName, WorkPolicyRevision: parent.WorkPolicyRevision,
		WorkKeyDigest: bytes.Clone(parent.WorkKeyDigest), WorkSequence: sequence,
		WorkFairnessDigest: bytes.Clone(parent.WorkFairnessDigest), WorkFairnessLimit: parent.WorkFairnessLimit,
		WorkExpiresAt: cloneEventReceiptTime(parent.WorkExpiresAt), StartDeadlineAt: cloneEventReceiptTime(parent.StartDeadlineAt),
		FailureRules: workpolicy.Clone(parent.FailureRules), RetryPolicyJSON: bytes.Clone(parent.RetryPolicyJSON),
		OnSuccessDestinationID: parent.OnSuccessDestinationID, OnFailureDestinationID: parent.OnFailureDestinationID,
		DeadlineAt: cloneEventReceiptTime(opts.DeadlineAt), ResultRetentionUntil: cloneEventReceiptTime(opts.ResultRetentionUntil),
		ReplayedFromInvocationID: parent.ID, ReplayRootInvocationID: parent.ReplayRootInvocationID,
		ReplayRootCreatedAt: cloneEventReceiptTime(parent.ReplayRootCreatedAt),
	}
	if len(opts.Headers) != 0 {
		inv.Headers = bytes.Clone(opts.Headers)
	}
	if inv.ReplayRootInvocationID == "" {
		inv.ReplayRootInvocationID = parent.ID
		inv.ReplayRootCreatedAt = cloneEventReceiptTime(&parent.CreatedAt)
	}
	return inv
}

func (s *PgStore) ReplayKeyedInvocation(ctx context.Context, accountID, parentID string, opts KeyedInvocationReplayOptions) (Invocation, error) {
	account, err := uuid.Parse(accountID)
	if err != nil {
		return Invocation{}, ErrNotFound
	}
	id, err := uuid.Parse(parentID)
	if err != nil {
		return Invocation{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invocation{}, fmt.Errorf("begin keyed replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	params := sqlc.KeyedReplayLaneIdentityParams{ID: mustPgUUID(id.String()), AccountID: mustPgUUID(account.String())}
	identity, err := q.KeyedReplayLaneIdentity(ctx, tx, params)
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	if !identity.WorkPolicyName.Valid {
		return Invocation{}, ErrKeyedReplayNotAllowed
	}
	lane := sqlc.KeyedReplayLockLaneParams{AppID: identity.AppID, PolicyName: identity.WorkPolicyName.String, KeyDigest: identity.WorkKeyDigest}
	// Match admission/claim lock order: lane first, then execution and owner.
	sequence, err := q.KeyedReplayLockLane(ctx, tx, lane)
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	row, err := q.KeyedReplayParent(ctx, tx, sqlc.KeyedReplayParentParams(params))
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	parent, err := invocationFromSQL(row)
	if err != nil {
		return Invocation{}, err
	}
	if !keyedReplayAllowed(parent) {
		return Invocation{}, ErrKeyedReplayNotAllowed
	}
	childID, err := q.KeyedReplayChildID(ctx, tx, row.ID)
	if err == nil {
		child, lookupErr := q.KeyedReplayChild(ctx, tx, childID)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return Invocation{}, ErrConflict // retention never authorizes a second child
		}
		if lookupErr != nil {
			return Invocation{}, fmt.Errorf("read keyed replay child: %w", lookupErr)
		}
		return ownedKeyedReplayChild(parent, child)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, fmt.Errorf("read keyed replay identity: %w", err)
	}
	expired, err := q.KeyedReplayExpired(ctx, tx, row.ID)
	if err != nil {
		return Invocation{}, fmt.Errorf("check keyed replay expiry: %w", err)
	}
	if expired.Bool {
		return Invocation{}, ErrKeyedReplayExpired
	}
	inv, err := enqueueInvocationRow(ctx, tx, newKeyedReplay(parent, opts, sequence, time.Now().UTC()))
	if err != nil {
		return Invocation{}, err
	}
	if err := q.KeyedReplayRecordChild(ctx, tx, sqlc.KeyedReplayRecordChildParams{ParentInvocationID: row.ID, ReplayInvocationID: mustPgUUID(inv.ID)}); err != nil {
		return Invocation{}, fmt.Errorf("record keyed replay: %w", err)
	}
	if err := q.KeyedReplayAdvanceLane(ctx, tx, sqlc.KeyedReplayAdvanceLaneParams(lane)); err != nil {
		return Invocation{}, fmt.Errorf("advance keyed replay lane: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, fmt.Errorf("commit keyed replay: %w", err)
	}
	return inv, nil
}

func (m *MemStore) ReplayKeyedInvocation(_ context.Context, accountID, parentID string, opts KeyedInvocationReplayOptions) (Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parent, ok := m.invocations[parentID]
	if !ok || !sameMemUUID(parent.AccountID, accountID) || !sameMemUUID(m.apps[parent.AppID].AccountID, accountID) {
		return Invocation{}, ErrNotFound
	}
	if !keyedReplayAllowed(parent) {
		return Invocation{}, ErrKeyedReplayNotAllowed
	}
	if childID := m.keyedReplayChildren[parentID]; childID != "" {
		child, ok := m.invocations[childID]
		if !ok {
			return Invocation{}, ErrConflict
		}
		if !invocationReplayChildMatches(parent, child) {
			return Invocation{}, ErrConflict
		}
		return cloneInvocationReplay(child), nil
	}
	now := time.Now().UTC()
	if parent.WorkExpiresAt != nil && !parent.WorkExpiresAt.After(now) || parent.StartDeadlineAt != nil && !parent.StartDeadlineAt.After(now) {
		return Invocation{}, ErrKeyedReplayExpired
	}
	sequence := parent.WorkSequence + 1
	for _, existing := range m.invocations {
		if existing.AppID == parent.AppID && existing.WorkPolicyName == parent.WorkPolicyName &&
			bytes.Equal(existing.WorkKeyDigest, parent.WorkKeyDigest) && existing.WorkSequence >= sequence {
			sequence = existing.WorkSequence + 1
		}
	}
	inv := newKeyedReplay(parent, opts, sequence, now)
	if err := m.platformTenantInvocationAllowedLocked(inv); err != nil {
		return Invocation{}, err
	}
	if _, err := jsonOrEmpty(inv.Headers); err != nil {
		return Invocation{}, err
	}
	if m.keyedReplayChildren == nil {
		m.keyedReplayChildren = make(map[string]string)
	}
	slot, err := m.eventReplayCapacityLocked(parent.ID)
	if err != nil {
		return Invocation{}, err
	}
	m.setInvocationLocked(inv.ID, inv)
	if slot.AccountID != "" {
		m.recordEventDeliverySlotLocked(inv.ID, slot)
	}
	m.keyedReplayChildren[parentID] = inv.ID
	return cloneInvocationReplay(inv), nil
}

func invocationReplayChildMatches(parent, child Invocation) bool {
	return sameMemUUID(parent.AccountID, child.AccountID) && sameMemUUID(parent.AppID, child.AppID) &&
		child.Source == InvocationReplay && sameMemUUID(parent.ID, child.ReplayedFromInvocationID)
}

func ownedKeyedReplayChild(parent Invocation, row sqlc.Invocation) (Invocation, error) {
	child, err := invocationFromSQL(row)
	if err != nil {
		return Invocation{}, err
	}
	if !invocationReplayChildMatches(parent, child) {
		return Invocation{}, ErrConflict
	}
	return child, nil
}

func cloneInvocationReplay(inv Invocation) Invocation {
	inv.Payload, inv.Headers = bytes.Clone(inv.Payload), bytes.Clone(inv.Headers)
	inv.WorkKeyDigest, inv.WorkFairnessDigest = bytes.Clone(inv.WorkKeyDigest), bytes.Clone(inv.WorkFairnessDigest)
	inv.WorkExpiresAt, inv.StartDeadlineAt = cloneEventReceiptTime(inv.WorkExpiresAt), cloneEventReceiptTime(inv.StartDeadlineAt)
	inv.ReplayRootCreatedAt = cloneEventReceiptTime(inv.ReplayRootCreatedAt)
	inv.DeadlineAt, inv.ResultRetentionUntil = cloneEventReceiptTime(inv.DeadlineAt), cloneEventReceiptTime(inv.ResultRetentionUntil)
	inv.RetryPolicyJSON, inv.FailureRules = bytes.Clone(inv.RetryPolicyJSON), workpolicy.Clone(inv.FailureRules)
	inv.Result, inv.WorkDecision = bytes.Clone(inv.Result), workpolicy.Clone(inv.WorkDecision)
	inv.CompletedAt, inv.ReceivedAt = cloneEventReceiptTime(inv.CompletedAt), cloneEventReceiptTime(inv.ReceivedAt)
	inv.LeaseExpiresAt, inv.LastReplayedAt = cloneEventReceiptTime(inv.LeaseExpiresAt), cloneEventReceiptTime(inv.LastReplayedAt)
	if inv.Outcome != nil {
		outcome := *inv.Outcome
		inv.Outcome = &outcome
	}
	return inv
}
