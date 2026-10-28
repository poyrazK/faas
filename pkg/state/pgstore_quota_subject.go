package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Per-account plan allowances belong to the billing subject of an app
// (ADR-202610171904): its organization once the organization adopted
// billing, and otherwise the account that created it, whose personal apps and
// unadopted company apps keep sharing one allowance. A company app keeps its
// creator's account_id, so counting rows by account_id would spend an adopted
// organization's allowance on its creator's personal one, and leave the
// organization's other creators uncounted.

// quotaSubjectSQL is the billing subject of the app aliased a, whose
// organization is left-joined as o.
const quotaSubjectSQL = `(case when not o.personal_org and o.resource_ownership_enabled then o.id else a.account_id end)`

// lockQuotaSubjectTx locks app's billing owner as runtime admission does
// (lock_app_resource_account): the organization row, then the creator's
// account unless the organization adopted billing, then the app, rechecked
// under those locks. It returns the subject whose allowances the app's
// resources share, and the app's account_id.
func lockQuotaSubjectTx(ctx context.Context, tx pgx.Tx, appID string) (subject, accountID string, err error) {
	var orgID pgtype.UUID
	err = tx.QueryRow(ctx, `select account_id::text, org_id from apps where id = $1 and status <> 'deleted'`, appID).Scan(&accountID, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("state: read app %s billing owner: %w", appID, err)
	}
	subject = accountID
	if orgID.Valid {
		var adopted bool
		if err := tx.QueryRow(ctx, `select not personal_org and resource_ownership_enabled from orgs where id = $1 for update`, orgID).Scan(&adopted); err != nil {
			return "", "", fmt.Errorf("state: lock app %s organization: %w", appID, err)
		}
		if adopted {
			subject = pgUUIDString(orgID)
		}
	}
	if subject == accountID {
		if _, err := tx.Exec(ctx, `select 1 from accounts where id = $1 for update`, accountID); err != nil {
			return "", "", fmt.Errorf("state: lock account %s: %w", accountID, err)
		}
	}
	var locked int
	err = tx.QueryRow(ctx, `select 1 from apps where id = $1 and account_id = $2 and org_id is not distinct from $3 and status <> 'deleted' for update`,
		appID, accountID, orgID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("state: lock app %s: %w", appID, err)
	}
	return subject, accountID, nil
}

// lockAccountQuotaSubjectTx locks an account for a create that belongs to
// the account itself, such as an account-wide alert rule; the account is the
// subject of its personal allowances.
func lockAccountQuotaSubjectTx(ctx context.Context, tx pgx.Tx, accountID string) error {
	var locked int
	err := tx.QueryRow(ctx, `select 1 from accounts where id = $1 for update`, accountID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: lock account %s: %w", accountID, err)
	}
	return nil
}
