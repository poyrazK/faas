package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ OrgActivityAccessMutationStore = (*PgStore)(nil)

func (s *PgStore) CreateOrgInvitationWithActivity(ctx context.Context, invitation OrgInvitation, activity OrgActivity) (OrgInvitation, int64, error) {
	if invitation.ID == "" {
		invitation.ID = uuid.NewString()
	}
	if invitation.CreatedAt.IsZero() {
		invitation.CreatedAt = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OrgInvitation{}, 0, fmt.Errorf("state: begin invitation activity create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		insert into org_invitations (
			id, org_id, email, role, token_hash, invited_by_account_id,
			expires_at, created_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8)
	`, invitation.ID, invitation.OrgID, invitation.Email, string(invitation.Role), invitation.TokenHash,
		invitation.InvitedByAccountID, invitation.ExpiresAt, invitation.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return OrgInvitation{}, 0, ErrConflict
		}
		return OrgInvitation{}, 0, fmt.Errorf("state: create org invitation with activity: %w", err)
	}
	activity, err = withOrgActivityData(activity, map[string]any{
		"role": string(invitation.Role), "expires_at": invitation.ExpiresAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	activity, err = bindOrgActivityToInvitation(activity, invitation)
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, activity)
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrgInvitation{}, 0, fmt.Errorf("state: commit invitation activity create: %w", err)
	}
	return invitation, outboxID, nil
}

func (s *PgStore) ConsumeOrgInvitationWithActivity(ctx context.Context, hash []byte, accepting Account, acceptedActivity, memberActivity OrgActivity) (OrgMembership, OrgInvitation, int64, int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, fmt.Errorf("state: consume org invitation activity tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inv, err := scanOrgInvitation(tx.QueryRow(ctx, `
		select id, org_id, email::text, role, token_hash, invited_by_account_id,
		       expires_at, consumed_at, revoked_at, accepting_account_id, created_at
		  from org_invitations where token_hash = $1 for update
	`, hash))
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	now := time.Now().UTC()
	if inv.ConsumedAt != nil || inv.RevokedAt != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgInvitationInvalid
	}
	if inv.ExpiresAt.Before(now) {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgInvitationExpired
	}
	if !strings.EqualFold(inv.Email, accepting.Email) {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgInvitationInvalid
	}

	var planSlug string
	if err := tx.QueryRow(ctx, `select plan from orgs where id = $1`, inv.OrgID).Scan(&planSlug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrgMembership{}, OrgInvitation{}, 0, 0, ErrNotFound
		}
		return OrgMembership{}, OrgInvitation{}, 0, 0, fmt.Errorf("state: consume org invitation activity plan: %w", err)
	}
	var activeMembers int
	if err := tx.QueryRow(ctx, `select count(*) from org_memberships where org_id = $1 and removed_at is null`, inv.OrgID).Scan(&activeMembers); err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, fmt.Errorf("state: consume org invitation activity count: %w", err)
	}
	limits, _ := api.LimitsFor(api.Plan(planSlug))
	if limit := limits.OrgMembersMax; limit > 0 && activeMembers >= limit {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgMemberCapExceeded
	}

	var existing string
	err = tx.QueryRow(ctx, `
		select account_id from org_memberships
		 where org_id = $1 and account_id = $2 and removed_at is null
	`, inv.OrgID, accepting.ID).Scan(&existing)
	switch {
	case err == nil:
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgAlreadyMember
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return OrgMembership{}, OrgInvitation{}, 0, 0, fmt.Errorf("state: consume org invitation activity existing member: %w", err)
	}

	var inviter *string
	if inv.InvitedByAccountID != nil {
		id := *inv.InvitedByAccountID
		inviter = &id
	}
	member, err := scanOrgMembership(tx.QueryRow(ctx, `
		insert into org_memberships (org_id, account_id, role, invited_by_account_id)
		values ($1, $2, $3, $4)
		returning org_id, account_id, role, invited_by_account_id, joined_at, removed_at
	`, inv.OrgID, accepting.ID, string(inv.Role), inviter))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgAlreadyMember
		}
		return OrgMembership{}, OrgInvitation{}, 0, 0, fmt.Errorf("state: consume org invitation activity insert member: %w", err)
	}
	if _, err := tx.Exec(ctx, `update org_invitations set consumed_at = $2, accepting_account_id = $3 where id = $1`, inv.ID, now, accepting.ID); err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, fmt.Errorf("state: consume org invitation activity stamp: %w", err)
	}
	acceptingID := accepting.ID
	inv.ConsumedAt = &now
	inv.AcceptingAccountID = &acceptingID

	acceptedActivity, err = withOrgActivityData(acceptedActivity, map[string]any{
		"role": string(inv.Role), "invitation_id": inv.ID,
	})
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	acceptedActivity, err = bindOrgActivityToInvitation(acceptedActivity, inv)
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	memberActivity, err = withOrgActivityData(memberActivity, map[string]any{
		"role": string(member.Role), "invitation_id": inv.ID,
	})
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	memberActivity, err = bindOrgActivityToMembership(memberActivity, member, accepting.Email)
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	acceptedOutboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, acceptedActivity)
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	memberOutboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, memberActivity)
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, fmt.Errorf("state: commit org invitation activity accept: %w", err)
	}
	return member, inv, acceptedOutboxID, memberOutboxID, nil
}

func (s *PgStore) RevokeOrgInvitationWithActivity(ctx context.Context, orgID, invitationID, _ string, activity OrgActivity) (OrgInvitation, int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OrgInvitation{}, 0, fmt.Errorf("state: revoke org invitation activity tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inv, err := scanOrgInvitation(tx.QueryRow(ctx, `
		select id, org_id, email::text, role, token_hash, invited_by_account_id,
		       expires_at, consumed_at, revoked_at, accepting_account_id, created_at
		  from org_invitations where id = $1 and org_id = $2 for update
	`, invitationID, orgID))
	if errors.Is(err, ErrNotFound) {
		return OrgInvitation{}, 0, ErrOrgInvitationInvalid
	}
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	if inv.ConsumedAt != nil || inv.RevokedAt != nil {
		return OrgInvitation{}, 0, ErrOrgInvitationInvalid
	}
	var revokedAt time.Time
	if err := tx.QueryRow(ctx, `update org_invitations set revoked_at = now() where id = $1 returning revoked_at`, inv.ID).Scan(&revokedAt); err != nil {
		return OrgInvitation{}, 0, fmt.Errorf("state: revoke org invitation activity update: %w", err)
	}
	inv.RevokedAt = &revokedAt
	activity, err = withOrgActivityData(activity, map[string]any{"role": string(inv.Role)})
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	activity, err = bindOrgActivityToInvitation(activity, inv)
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, activity)
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrgInvitation{}, 0, fmt.Errorf("state: commit org invitation activity revoke: %w", err)
	}
	return inv, outboxID, nil
}

func (s *PgStore) UpdateOrgMemberRoleWithActivity(ctx context.Context, orgID, accountID string, role OrgRole, activity OrgActivity) (OrgMembership, int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OrgMembership{}, 0, fmt.Errorf("state: update org member role activity tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanOrgMembership(tx.QueryRow(ctx, `
		select org_id, account_id, role, invited_by_account_id, joined_at, removed_at
		  from org_memberships where org_id = $1 and account_id = $2 for update
	`, orgID, accountID))
	if err != nil {
		return OrgMembership{}, 0, err
	}
	if current.Role == OrgRoleOwner && role != OrgRoleOwner && current.RemovedAt == nil {
		return OrgMembership{}, 0, ErrOrgLastOwner
	}
	if current.Role == role {
		if err := tx.Commit(ctx); err != nil {
			return OrgMembership{}, 0, fmt.Errorf("state: commit unchanged org member role: %w", err)
		}
		return current, 0, nil
	}
	updated, err := scanOrgMembership(tx.QueryRow(ctx, `
		update org_memberships set role = $3 where org_id = $1 and account_id = $2
		returning org_id, account_id, role, invited_by_account_id, joined_at, removed_at
	`, orgID, accountID, string(role)))
	if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation:
			return OrgMembership{}, 0, ErrOrgLastOwner
		case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.CheckViolation:
			return OrgMembership{}, 0, fmt.Errorf("state: update org member role activity check: %w", err)
		}
		return OrgMembership{}, 0, fmt.Errorf("state: update org member role activity update: %w", err)
	}
	label, err := orgMemberActivityLabelTx(ctx, tx, accountID)
	if err != nil {
		return OrgMembership{}, 0, err
	}
	activity, err = withOrgActivityData(activity, map[string]any{
		"old_role": string(current.Role), "new_role": string(updated.Role),
	})
	if err != nil {
		return OrgMembership{}, 0, err
	}
	activity, err = bindOrgActivityToMembership(activity, updated, label)
	if err != nil {
		return OrgMembership{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, activity)
	if err != nil {
		return OrgMembership{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrgMembership{}, 0, fmt.Errorf("state: commit org member role activity: %w", err)
	}
	return updated, outboxID, nil
}

func (s *PgStore) RemoveOrgMemberWithActivity(ctx context.Context, orgID, accountID string, activity OrgActivity) (OrgMembership, int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OrgMembership{}, 0, fmt.Errorf("state: remove org member activity tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanOrgMembership(tx.QueryRow(ctx, `
		select org_id, account_id, role, invited_by_account_id, joined_at, removed_at
		  from org_memberships where org_id = $1 and account_id = $2 for update
	`, orgID, accountID))
	if err != nil {
		return OrgMembership{}, 0, err
	}
	if current.Role == OrgRoleOwner && current.RemovedAt == nil {
		return OrgMembership{}, 0, ErrOrgLastOwner
	}
	if current.RemovedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return OrgMembership{}, 0, fmt.Errorf("state: commit already-removed org member: %w", err)
		}
		return current, 0, nil
	}
	removed, err := scanOrgMembership(tx.QueryRow(ctx, `
		update org_memberships set removed_at = now() where org_id = $1 and account_id = $2
		returning org_id, account_id, role, invited_by_account_id, joined_at, removed_at
	`, orgID, accountID))
	if err != nil {
		return OrgMembership{}, 0, fmt.Errorf("state: remove org member activity update: %w", err)
	}
	label, err := orgMemberActivityLabelTx(ctx, tx, accountID)
	if err != nil {
		return OrgMembership{}, 0, err
	}
	activity, err = withOrgActivityData(activity, map[string]any{"role": string(removed.Role)})
	if err != nil {
		return OrgMembership{}, 0, err
	}
	activity, err = bindOrgActivityToMembership(activity, removed, label)
	if err != nil {
		return OrgMembership{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, activity)
	if err != nil {
		return OrgMembership{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrgMembership{}, 0, fmt.Errorf("state: commit org member removal activity: %w", err)
	}
	return removed, outboxID, nil
}

func orgMemberActivityLabelTx(ctx context.Context, tx pgx.Tx, accountID string) (string, error) {
	var email string
	err := tx.QueryRow(ctx, `select email::text from accounts where id = $1`, accountID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "member", nil
	}
	if err != nil {
		return "", fmt.Errorf("state: resolve org member activity label: %w", err)
	}
	return email, nil
}
