package state

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectStorageCeilingGrant is an object-storage ceiling support granted a
// billing subject, an account or a resource-owning org, in place of its paid
// plan's (ADR-20261101214152). The subject's spend cap still tightens it, and
// a Free subject ignores it.
type ObjectStorageCeilingGrant struct {
	OwnerID   string
	MaxBytes  int64
	Reason    string
	GrantedBy string
	GrantedAt time.Time
}

// ObjectStorageCeilingGrantStore holds the grants. apid is the only writer.
type ObjectStorageCeilingGrantStore interface {
	// ObjectStorageCeilingGrant returns the owner's grant, nil when it has none.
	ObjectStorageCeilingGrant(ctx context.Context, ownerID string) (*ObjectStorageCeilingGrant, error)
	// SetObjectStorageCeilingGrant writes the owner's grant, replacing any it had.
	SetObjectStorageCeilingGrant(ctx context.Context, grant ObjectStorageCeilingGrant) error
	// ClearObjectStorageCeilingGrant removes the owner's grant, if it has one.
	ClearObjectStorageCeilingGrant(ctx context.Context, ownerID string) error
}

var objectStorageCeilingGrantReason = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// validObjectStorageCeilingGrant holds a grant to the table's CHECKs.
func validObjectStorageCeilingGrant(g ObjectStorageCeilingGrant) error {
	if g.MaxBytes <= 0 || g.MaxBytes > api.MaxObjectStoragePolicyValue {
		return fmt.Errorf("object storage ceiling grant: max bytes %d out of range", g.MaxBytes)
	}
	if !objectStorageCeilingGrantReason.MatchString(g.Reason) {
		return fmt.Errorf("object storage ceiling grant: reason %q must match [a-z0-9_]{1,64}", g.Reason)
	}
	if g.GrantedAt.IsZero() {
		return fmt.Errorf("object storage ceiling grant: granted_at is required")
	}
	return nil
}

// ObjectStorageCeilingGrant implements ObjectStorageCeilingGrantStore.
func (s *PgStore) ObjectStorageCeilingGrant(ctx context.Context, ownerID string) (*ObjectStorageCeilingGrant, error) {
	owner, err := parsePgUUID(ownerID)
	if err != nil {
		return nil, err
	}
	row, err := sqlc.New().ObjectStorageCeilingGrant(ctx, s.pool, owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read object storage ceiling grant: %w", err)
	}
	return &ObjectStorageCeilingGrant{OwnerID: pgUUIDString(row.OwnerID), MaxBytes: row.MaxBytes, Reason: row.Reason,
		GrantedBy: pgUUIDString(row.GrantedBy), GrantedAt: row.GrantedAt.Time}, nil
}

// SetObjectStorageCeilingGrant implements ObjectStorageCeilingGrantStore.
func (s *PgStore) SetObjectStorageCeilingGrant(ctx context.Context, g ObjectStorageCeilingGrant) error {
	if err := validObjectStorageCeilingGrant(g); err != nil {
		return err
	}
	owner, err := parsePgUUID(g.OwnerID)
	if err != nil {
		return err
	}
	by, err := parsePgUUID(g.GrantedBy)
	if err != nil {
		return err
	}
	if err := sqlc.New().SetObjectStorageCeilingGrant(ctx, s.pool, sqlc.SetObjectStorageCeilingGrantParams{
		OwnerID: owner, MaxBytes: g.MaxBytes, Reason: g.Reason, GrantedBy: by,
		GrantedAt: pgtype.Timestamptz{Time: g.GrantedAt.UTC(), Valid: true},
	}); err != nil {
		return fmt.Errorf("write object storage ceiling grant: %w", err)
	}
	return nil
}

// ClearObjectStorageCeilingGrant implements ObjectStorageCeilingGrantStore.
func (s *PgStore) ClearObjectStorageCeilingGrant(ctx context.Context, ownerID string) error {
	owner, err := parsePgUUID(ownerID)
	if err != nil {
		return err
	}
	if err := sqlc.New().ClearObjectStorageCeilingGrant(ctx, s.pool, owner); err != nil {
		return fmt.Errorf("clear object storage ceiling grant: %w", err)
	}
	return nil
}

// ObjectStorageCeilingGrant implements ObjectStorageCeilingGrantStore.
func (m *MemStore) ObjectStorageCeilingGrant(_ context.Context, ownerID string) (*ObjectStorageCeilingGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.objectStorageCeilingGrants[ownerID]
	if !ok {
		return nil, nil
	}
	return &g, nil
}

// SetObjectStorageCeilingGrant implements ObjectStorageCeilingGrantStore.
func (m *MemStore) SetObjectStorageCeilingGrant(_ context.Context, g ObjectStorageCeilingGrant) error {
	if err := validObjectStorageCeilingGrant(g); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objectStorageCeilingGrants == nil {
		m.objectStorageCeilingGrants = map[string]ObjectStorageCeilingGrant{}
	}
	g.GrantedAt = g.GrantedAt.UTC()
	m.objectStorageCeilingGrants[g.OwnerID] = g
	return nil
}

// ClearObjectStorageCeilingGrant implements ObjectStorageCeilingGrantStore.
func (m *MemStore) ClearObjectStorageCeilingGrant(_ context.Context, ownerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objectStorageCeilingGrants, ownerID)
	return nil
}
