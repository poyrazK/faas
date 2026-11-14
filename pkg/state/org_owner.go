package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrOrgOwnerInactive refuses to make an account an organization's owner
// while it is suspended or pending deletion (ADR 20261113180000): its
// deletion could never complete and nobody could use the organization.
var ErrOrgOwnerInactive = errors.New("state: only an active account can own an organization")

// OrgOwnerAssigner lets an operator give an organization a new owner when
// its owner cannot act: suspended, pending deletion or locked out (ADR
// 20261113180000). The new owner must be an active member with an active
// account; any current owner becomes an admin.
type OrgOwnerAssigner interface {
	AssignOrgOwner(ctx context.Context, orgID, accountID string) (previousOwner string, err error)
}

var (
	_ OrgOwnerAssigner = (*MemStore)(nil)
	_ OrgOwnerAssigner = (*PgStore)(nil)
)

// mayOwnOrgLocked mirrors the org_owner_account_active trigger.
func (m *MemStore) mayOwnOrgLocked(accountID string) bool {
	acct, ok := m.accounts[accountID]
	return ok && (acct.Status == AccountActive || acct.Status == AccountPastDue)
}

func (m *MemStore) AssignOrgOwner(_ context.Context, orgID, accountID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.orgs[orgID]
	if !ok || org.Personal {
		return "", ErrNotFound
	}
	target, ok := m.memberships[orgAccountKey{OrgID: orgID, AccountID: accountID}]
	if !ok || target.RemovedAt != nil {
		return "", ErrNotFound
	}
	if target.Role == OrgRoleOwner {
		return accountID, nil
	}
	if !m.mayOwnOrgLocked(accountID) {
		return "", ErrOrgOwnerInactive
	}
	previous := ""
	for key, member := range m.memberships {
		if member.OrgID == orgID && member.Role == OrgRoleOwner && member.RemovedAt == nil {
			previous = member.AccountID
			member.Role = OrgRoleAdmin
			m.setOrgMembershipLocked(key, member)
		}
	}
	target.Role = OrgRoleOwner
	m.setOrgMembershipLocked(orgAccountKey{OrgID: orgID, AccountID: accountID}, target)
	return previous, nil
}

func (s *PgStore) AssignOrgOwner(ctx context.Context, orgID, accountID string) (string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("state: assign org owner tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after Commit
	var personal bool
	if err := tx.QueryRow(ctx, `select personal_org from orgs where id = $1 for update`, orgID).Scan(&personal); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("state: assign org owner lock org: %w", mapErr(err))
	}
	if personal {
		return "", ErrNotFound
	}
	var role string
	err = tx.QueryRow(ctx, `
		select role from org_memberships
		 where org_id = $1 and account_id = $2 and removed_at is null
		   for update`, orgID, accountID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: assign org owner lock member: %w", mapErr(err))
	}
	if role == string(OrgRoleOwner) {
		return accountID, tx.Commit(ctx)
	}
	var previous string
	err = tx.QueryRow(ctx, `
		update org_memberships set role = 'admin'
		 where org_id = $1 and role = 'owner' and removed_at is null
		returning account_id::text`, orgID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("state: assign org owner demote: %w", mapErr(err))
	}
	if _, err := tx.Exec(ctx, `
		update org_memberships set role = 'owner'
		 where org_id = $1 and account_id = $2 and removed_at is null`, orgID, accountID); err != nil {
		return "", fmt.Errorf("state: assign org owner promote: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("state: assign org owner commit: %w", err)
	}
	return previous, nil
}
