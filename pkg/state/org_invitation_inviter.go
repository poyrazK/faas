package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// An invitation carries its inviter's authority only while the inviter
// holds it. An admin about to be removed could invite a second address as
// admin and, once removed, accept it within the invitation's 14 days
// (account-flow audit 2026-11-10, ADR 20261111130000). Accepting therefore
// requires the inviter to still be an active owner or admin. An invitation
// without a recorded inviter (legacy rows) keeps its old behaviour.
func inviterMayInvite(role OrgRole) bool {
	return role == OrgRoleOwner || role == OrgRoleAdmin
}

// checkInviterTx refuses an invitation whose inviter left the organization
// or no longer holds a role that may invite.
func checkInviterTx(ctx context.Context, tx pgx.Tx, inv OrgInvitation) error {
	if inv.InvitedByAccountID == nil || *inv.InvitedByAccountID == "" {
		return nil
	}
	var role string
	err := tx.QueryRow(ctx, `
		select role from org_memberships
		 where org_id = $1 and account_id = $2 and removed_at is null
	`, inv.OrgID, *inv.InvitedByAccountID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOrgInvitationInvalid
	}
	if err != nil {
		return fmt.Errorf("state: consume org invitation inviter: %w", err)
	}
	if !inviterMayInvite(OrgRole(role)) {
		return ErrOrgInvitationInvalid
	}
	return nil
}

// checkInviterLocked mirrors checkInviterTx; m.mu is held.
func (m *MemStore) checkInviterLocked(inv OrgInvitation) error {
	if inv.InvitedByAccountID == nil || *inv.InvitedByAccountID == "" {
		return nil
	}
	member, ok := m.memberships[orgAccountKey{OrgID: inv.OrgID, AccountID: *inv.InvitedByAccountID}]
	if !ok || member.RemovedAt != nil || !inviterMayInvite(member.Role) {
		return ErrOrgInvitationInvalid
	}
	return nil
}
