package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SharedOrgCreationStore commits the account's Free-org limit, org and initial
// owner together. Keeping this optional avoids widening the legacy Store seam.
type SharedOrgCreationStore interface {
	CreateSharedOrg(context.Context, string, string, string) (Org, error)
}

// OrgFreeLimitError preserves the observed ownership count for the API problem.
type OrgFreeLimitError struct{ Owned int }

func (e *OrgFreeLimitError) Error() string {
	return "state: Free organization ownership limit exceeded"
}

var _ SharedOrgCreationStore = (*MemStore)(nil)
var _ SharedOrgCreationStore = (*PgStore)(nil)

func (s *PgStore) CreateSharedOrg(ctx context.Context, ownerID, slug, name string) (Org, error) {
	owner, err := parsePgUUID(ownerID)
	if err != nil {
		return Org{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Org{}, err
	}
	defer rollbackOrgAccess(ctx, tx)
	q := sqlc.New()
	if _, err = q.LockSharedOrgCreator(ctx, tx, owner); err != nil {
		return Org{}, mapErr(err)
	}
	owned, err := q.CountOwnedFreeSharedOrgs(ctx, tx, owner)
	if err != nil {
		return Org{}, fmt.Errorf("state: count owned Free orgs: %w", err)
	}
	if owned >= int64(api.FreeOrganizationsOwnedMax) {
		return Org{}, &OrgFreeLimitError{Owned: int(owned)}
	}
	id := mustPgUUID(uuid.NewString())
	row, err := q.InsertFreeSharedOrg(ctx, tx, sqlc.InsertFreeSharedOrgParams{ID: id, Slug: slug, Name: name})
	if err != nil {
		return Org{}, mapErr(err)
	}
	if err = q.InsertSharedOrgOwner(ctx, tx, sqlc.InsertSharedOrgOwnerParams{OrgID: id, AccountID: owner}); err != nil {
		return Org{}, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Org{}, fmt.Errorf("state: commit shared org: %w", err)
	}
	return Org{ID: pgUUIDString(row.ID), Slug: row.Slug, Name: row.Name, Plan: api.Plan(row.Plan),
		Status: OrgStatus(row.Status), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		ResourceOwnershipEnabled: row.ResourceOwnershipEnabled}, nil
}

func (m *MemStore) CreateSharedOrg(ctx context.Context, ownerID, slug, name string) (Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return Org{}, err
	}
	if _, exists := m.accounts[ownerID]; !exists {
		return Org{}, ErrNotFound
	}
	owned := 0
	for _, org := range m.orgs {
		member := m.memberships[orgAccountKey{OrgID: org.ID, AccountID: ownerID}]
		if !org.Personal && !org.DeletedPending && org.Plan == api.PlanFree && member.Role == OrgRoleOwner && member.RemovedAt == nil {
			owned++
		}
	}
	if owned >= api.FreeOrganizationsOwnedMax {
		return Org{}, &OrgFreeLimitError{Owned: owned}
	}
	org, err := m.createOrgLocked(Org{Slug: slug, Name: name})
	if err != nil {
		return Org{}, err
	}
	// The new org has no memberships; its owner cannot conflict.
	m.setOrgMembershipLocked(orgAccountKey{OrgID: org.ID, AccountID: ownerID}, OrgMembership{
		OrgID: org.ID, AccountID: ownerID, Role: OrgRoleOwner, InvitedByAccountID: &ownerID, JoinedAt: org.CreatedAt,
	})
	return org, nil
}

func orgInvitationLimit(plan api.Plan, status OrgStatus, deleted, held bool) (int, error) {
	if deleted || status != OrgStatusActive || held {
		return 0, ErrOrgInvitationInvalid
	}
	limit := plan.OrgMembersMax()
	if limit <= 0 {
		return 0, ErrOrgMemberCapExceeded
	}
	return limit, nil
}

// Lock before counting: invitation row locks alone do not protect the last seat
// from another invitation, or keep a downgrade/deletion from racing acceptance.
func lockOrgInvitationLimit(ctx context.Context, tx pgx.Tx, orgID string) (int, error) {
	row, err := sqlc.New().LockOrgInvitationTarget(ctx, tx, mustPgUUID(orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("state: lock invitation target: %w", err)
	}
	return orgInvitationLimit(api.Plan(row.Plan), OrgStatus(row.Status), row.DeletedPending, row.AbuseHoldAt.Valid)
}
