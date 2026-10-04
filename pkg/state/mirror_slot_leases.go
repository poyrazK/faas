package state

import (
	"context"
	"time"
)

// MirrorSlotLeaseStore coordinates mirror admission across gateway replicas.
// Implementations use expiry to recover reservations left by a crashed
// gateway and make release idempotent so deferred cleanup is safe to retry.
type MirrorSlotLeaseStore interface {
	TryAcquireMirrorSlotLease(ctx context.Context, ruleID string, limit int, ttl time.Duration) (leaseID string, acquired bool, err error)
	ReleaseMirrorSlotLease(ctx context.Context, ruleID, leaseID string) error
}

// MirrorSlotLease is the in-memory representation of one live shadow-VM
// reservation. The production table stores the same fields durably.
type MirrorSlotLease struct {
	LeaseID   string
	RuleID    string
	ExpiresAt time.Time
}

var (
	_ MirrorSlotLeaseStore = (*PgStore)(nil)
	_ MirrorSlotLeaseStore = (*MemStore)(nil)
)
