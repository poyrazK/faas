package support

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TicketOwner identifies the customer behind a ticket for the staff inbox's
// account panel (ADR-20261112170000). AccountID is the personal owner, or
// for an organization case the account that opened it; OrgID is set for
// organization cases. Callers must already have authorized the staff actor
// for this ticket (Portal does). It returns empty strings when the ticket
// does not exist or its opener was deleted.
func (s *Service) TicketOwner(ctx context.Context, ticketID string) (accountID, orgID string, err error) {
	if s == nil || s.Pool == nil {
		return "", "", errors.New("support: unavailable")
	}
	var account, org, creator pgtype.UUID
	err = s.Pool.QueryRow(ctx, `SELECT account_id, org_id, created_by FROM support_tickets WHERE id=$1`, uid(ticketID)).
		Scan(&account, &org, &creator)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil
		}
		return "", "", err
	}
	text := func(v pgtype.UUID) string {
		if !v.Valid {
			return ""
		}
		value, _ := v.Value()
		out, _ := value.(string)
		return out
	}
	accountID, orgID = text(account), text(org)
	if accountID == "" {
		accountID = text(creator)
	}
	return accountID, orgID, nil
}
