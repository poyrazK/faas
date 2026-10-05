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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrPlainReplayNotAllowed = errors.New("state: plain replay requires failed or dead-lettered unbound unkeyed work")

// Identity, request and lineage come from the stored parent. The API refreshes
// transport/version headers, retry policy and execution/result lifetimes.
type PlainInvocationReplayOptions struct {
	Headers              json.RawMessage
	RetryPolicyJSON      json.RawMessage
	DeadlineAt           *time.Time
	ResultRetentionUntil *time.Time
}

type PlainInvocationReplayStore interface {
	ExistingPlainInvocationReplay(context.Context, string, string) (Invocation, error)
	ReplayPlainInvocation(context.Context, string, string, PlainInvocationReplayOptions) (Invocation, error)
}

type plainReplayIdentity struct {
	ChildID   string
	CreatedAt time.Time
}

func plainReplayAllowed(parent Invocation) bool {
	return (parent.State == InvocationFailed || parent.State == InvocationDeadLetter) &&
		parent.WorkPolicyName == "" && parent.QueueBindingID == "" && parent.QueueName == "" && parent.EnvironmentID == "" && !InvocationHasOperation(parent)
}

func (s *PgStore) beginPlainReplay(ctx context.Context, accountID, parentID string) (pgx.Tx, Invocation, error) {
	account, err := uuid.Parse(accountID)
	if err != nil {
		return nil, Invocation{}, ErrNotFound
	}
	id, err := uuid.Parse(parentID)
	if err != nil {
		return nil, Invocation{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, Invocation{}, fmt.Errorf("begin plain replay: %w", err)
	}
	row, err := sqlc.New().PlainReplayParent(ctx, tx, sqlc.PlainReplayParentParams{ID: mustPgUUID(id.String()), AccountID: mustPgUUID(account.String())})
	var parent Invocation
	if err == nil {
		parent, err = invocationFromSQL(row)
		if err == nil && !plainReplayAllowed(parent) {
			err = ErrPlainReplayNotAllowed
		}
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, Invocation{}, mapErr(err)
	}
	return tx, parent, nil
}

// Duplicate reads precede new-admission checks (including expired deployment
// pins or a suspended customer) but always recheck current app ownership.
func (s *PgStore) ExistingPlainInvocationReplay(ctx context.Context, accountID, parentID string) (Invocation, error) {
	tx, parent, err := s.beginPlainReplay(ctx, accountID, parentID)
	if err != nil {
		return Invocation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return existingPgPlainReplay(ctx, tx, parent)
}

func existingPgPlainReplay(ctx context.Context, tx pgx.Tx, parent Invocation) (Invocation, error) {
	q := sqlc.New()
	identity, err := q.PlainReplayIdentity(ctx, tx, mustPgUUID(parent.ID))
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	row, err := q.KeyedReplayChild(ctx, tx, identity.ReplayInvocationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, ErrConflict
	}
	if err != nil {
		return Invocation{}, fmt.Errorf("read plain replay child: %w", err)
	}
	child, err := invocationFromSQL(row)
	if err != nil {
		return Invocation{}, err
	}
	return ownedPlainReplayChild(parent, child, timeFromPgtype(identity.ReplayCreatedAt))
}

func ownedPlainReplayChild(parent, child Invocation, createdAt time.Time) (Invocation, error) {
	root, rootCreated := parent.ReplayRootInvocationID, parent.ReplayRootCreatedAt
	if root == "" {
		root, rootCreated = parent.ID, &parent.CreatedAt
	}
	if !invocationReplayChildMatches(parent, child) || !child.CreatedAt.Equal(createdAt) ||
		child.DeploymentScope != parent.DeploymentScope || !sameMemUUID(child.PlatformTenantID, parent.PlatformTenantID) ||
		!sameMemUUID(child.ReplayRootInvocationID, root) || child.ReplayRootCreatedAt == nil || rootCreated == nil ||
		!child.ReplayRootCreatedAt.Equal(*rootCreated) {
		return Invocation{}, ErrConflict
	}
	return cloneInvocationReplay(child), nil
}

func (s *PgStore) ReplayPlainInvocation(ctx context.Context, accountID, parentID string, opts PlainInvocationReplayOptions) (Invocation, error) {
	tx, parent, err := s.beginPlainReplay(ctx, accountID, parentID)
	if err != nil {
		return Invocation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	child, err := existingPgPlainReplay(ctx, tx, parent)
	if !errors.Is(err, ErrNotFound) {
		return child, err
	}
	inv, err := enqueueInvocationRow(ctx, tx, newPlainReplay(parent, opts, time.Now().UTC()))
	if err != nil {
		return Invocation{}, fmt.Errorf("enqueue plain replay: %w", err)
	}
	params := sqlc.PlainReplayRecordChildParams{ParentInvocationID: mustPgUUID(parent.ID), ReplayInvocationID: mustPgUUID(inv.ID), ReplayCreatedAt: pgtype.Timestamptz{Time: inv.CreatedAt, Valid: true}}
	if err := sqlc.New().PlainReplayRecordChild(ctx, tx, params); err != nil {
		return Invocation{}, fmt.Errorf("record plain replay: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, fmt.Errorf("commit plain replay: %w", err)
	}
	return inv, nil
}

func newPlainReplay(parent Invocation, opts PlainInvocationReplayOptions, now time.Time) Invocation {
	inv := Invocation{
		ID: uuid.NewString(), AppID: parent.AppID, AccountID: parent.AccountID,
		DeploymentScope: parent.DeploymentScope, PlatformTenantID: parent.PlatformTenantID,
		Source: InvocationReplay, State: InvocationPending, Method: parent.Method, Path: parent.Path,
		Payload: bytes.Clone(parent.Payload), Headers: bytes.Clone(parent.Headers), DueAt: now, CreatedAt: now,
		RetryPolicyJSON: bytes.Clone(opts.RetryPolicyJSON),
		DeadlineAt:      cloneEventReceiptTime(opts.DeadlineAt), ResultRetentionUntil: cloneEventReceiptTime(opts.ResultRetentionUntil),
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

func (m *MemStore) plainReplayParentLocked(accountID, parentID string) (Invocation, error) {
	parent, ok := m.invocations[parentID]
	app, owned := m.eventSubscriptionAppLocked(parent.AppID)
	if !ok || !sameMemUUID(parent.AccountID, accountID) || !owned || !sameMemUUID(app.AccountID, accountID) {
		return Invocation{}, ErrNotFound
	}
	if _, _, linked := m.operationForInvocationLocked(parent.ID); linked {
		parent.OperationID = "owned"
	}
	if !plainReplayAllowed(parent) {
		return Invocation{}, ErrPlainReplayNotAllowed
	}
	return parent, nil
}

func (m *MemStore) existingPlainReplayLocked(parent Invocation) (Invocation, error) {
	identity, ok := m.plainReplayChildren[parent.ID]
	if !ok {
		return Invocation{}, ErrNotFound
	}
	child, ok := m.invocations[identity.ChildID]
	if !ok {
		return Invocation{}, ErrConflict
	}
	return ownedPlainReplayChild(parent, child, identity.CreatedAt)
}

func (m *MemStore) ExistingPlainInvocationReplay(_ context.Context, accountID, parentID string) (Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parent, err := m.plainReplayParentLocked(accountID, parentID)
	if err != nil {
		return Invocation{}, err
	}
	return m.existingPlainReplayLocked(parent)
}

func (m *MemStore) ReplayPlainInvocation(_ context.Context, accountID, parentID string, opts PlainInvocationReplayOptions) (Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parent, err := m.plainReplayParentLocked(accountID, parentID)
	if err != nil {
		return Invocation{}, err
	}
	child, err := m.existingPlainReplayLocked(parent)
	if !errors.Is(err, ErrNotFound) {
		return child, err
	}
	inv := newPlainReplay(parent, opts, time.Now().UTC())
	if err := m.platformTenantInvocationAllowedLocked(inv); err != nil {
		return Invocation{}, err
	}
	if _, err := jsonOrEmpty(inv.Headers); err != nil {
		return Invocation{}, err
	}
	if _, err := jsonOrEmpty(inv.RetryPolicyJSON); err != nil {
		return Invocation{}, err
	}
	if m.plainReplayChildren == nil {
		m.plainReplayChildren = make(map[string]plainReplayIdentity)
	}
	slot, err := m.eventReplayCapacityLocked(parent.ID)
	if err != nil {
		return Invocation{}, err
	}
	m.setInvocationLocked(inv.ID, inv)
	if slot.AccountID != "" {
		m.recordEventDeliverySlotLocked(inv.ID, slot)
	}
	m.plainReplayChildren[parent.ID] = plainReplayIdentity{ChildID: inv.ID, CreatedAt: inv.CreatedAt}
	return cloneInvocationReplay(inv), nil
}
