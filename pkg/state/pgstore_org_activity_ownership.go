package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ OrgActivityOwnershipTransferMutationStore = (*PgStore)(nil)

func (s *PgStore) TransferOrgOwnershipWithActivity(ctx context.Context, orgID, fromAccountID, toAccountID string, activity OrgActivity) (int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("state: transfer org ownership activity tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	from, to, err := transferOrgOwnershipTx(ctx, tx, orgID, fromAccountID, toAccountID)
	if err != nil {
		return 0, err
	}
	label, err := orgMemberActivityLabelTx(ctx, tx, to.AccountID)
	if err != nil {
		return 0, err
	}
	activity, err = bindOrgActivityToOwnershipTransfer(activity, from, to, label)
	if err != nil {
		return 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, activity)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit org ownership activity: %w", err)
	}
	return outboxID, nil
}

func transferOrgOwnershipTx(ctx context.Context, tx pgx.Tx, orgID, fromAccountID, toAccountID string) (OrgMembership, OrgMembership, error) {
	if fromAccountID == toAccountID {
		return OrgMembership{}, OrgMembership{}, ErrOrgLastOwner
	}
	from, err := scanOrgMembership(tx.QueryRow(ctx, `
		select org_id, account_id, role, invited_by_account_id, joined_at, removed_at
		  from org_memberships
		 where org_id = $1 and account_id = $2
		   for update
	`, orgID, fromAccountID))
	if err != nil {
		return OrgMembership{}, OrgMembership{}, err
	}
	if from.Role != OrgRoleOwner || from.RemovedAt != nil {
		return OrgMembership{}, OrgMembership{}, ErrOrgLastOwner
	}
	to, err := scanOrgMembership(tx.QueryRow(ctx, `
		select org_id, account_id, role, invited_by_account_id, joined_at, removed_at
		  from org_memberships
		 where org_id = $1 and account_id = $2
		   for update
	`, orgID, toAccountID))
	if err != nil {
		return OrgMembership{}, OrgMembership{}, err
	}
	if to.RemovedAt != nil {
		return OrgMembership{}, OrgMembership{}, ErrNotFound
	}
	if to.Role == OrgRoleOwner {
		return OrgMembership{}, OrgMembership{}, ErrOrgLastOwner
	}
	if err := checkTransferFreeOrgSlotTx(ctx, tx, orgID, toAccountID); err != nil {
		return OrgMembership{}, OrgMembership{}, err
	}

	// Demote first to preserve the one-active-owner partial unique index.
	if _, err := tx.Exec(ctx, `
		update org_memberships set role = $3 where org_id = $1 and account_id = $2
	`, orgID, fromAccountID, string(OrgRoleAdmin)); err != nil {
		return OrgMembership{}, OrgMembership{}, fmt.Errorf("state: transfer ownership demote: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update org_memberships set role = $3 where org_id = $1 and account_id = $2
	`, orgID, toAccountID, string(OrgRoleOwner)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return OrgMembership{}, OrgMembership{}, ErrOrgLastOwner
		}
		return OrgMembership{}, OrgMembership{}, fmt.Errorf("state: transfer ownership promote: %w", err)
	}
	from.Role = OrgRoleAdmin
	to.Role = OrgRoleOwner
	return from, to, nil
}

// checkTransferFreeOrgSlotTx refuses to make toAccountID the owner of a Free
// organization when it already owns FreeOrganizationsOwnedMax of them
// (ADR-20261104211412). The limit was checked only at creation, so creating
// an organization and handing it to another account multiplied the Free
// tier. The recipient's account row is locked as creation locks it.
func checkTransferFreeOrgSlotTx(ctx context.Context, tx pgx.Tx, orgID, toAccountID string) error {
	org, err := parsePgUUID(orgID)
	if err != nil {
		return err
	}
	to, err := parsePgUUID(toAccountID)
	if err != nil {
		return err
	}
	q := sqlc.New()
	takesSlot, err := q.FreeSharedOrgForTransfer(ctx, tx, org)
	if err != nil {
		return mapErr(err)
	}
	if !takesSlot {
		return nil
	}
	if _, err := q.LockSharedOrgCreator(ctx, tx, to); err != nil {
		return mapErr(err)
	}
	owned, err := q.CountOwnedFreeSharedOrgs(ctx, tx, to)
	if err != nil {
		return fmt.Errorf("state: count recipient's Free orgs: %w", err)
	}
	if owned >= int64(api.FreeOrganizationsOwnedMax) {
		return &OrgFreeLimitError{Owned: int(owned)}
	}
	return nil
}
