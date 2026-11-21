package support

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TicketCustomer identifies the customer behind a ticket and the request
// id they attached (ADR-20261112170000, ADR-20261112210000). AccountID is
// the personal owner, or for an organization case the account that opened
// it; OrgID is set for organization cases.
type TicketCustomer struct {
	AccountID, OrgID, RequestID string
}

// TicketCustomer loads who a ticket belongs to. Callers must already have
// authorized the reader for this ticket, or be acting on a ticket the
// caller just created. It returns a zero value when the ticket does not
// exist or its opener was deleted.
func (s *Service) TicketCustomer(ctx context.Context, ticketID string) (TicketCustomer, error) {
	if s == nil || s.Pool == nil {
		return TicketCustomer{}, errors.New("support: unavailable")
	}
	var (
		account, org, creator pgtype.UUID
		requestID             string
	)
	err := s.Pool.QueryRow(ctx, `SELECT account_id, org_id, created_by, coalesce(state->'context'->>'request_id','')
		FROM support_tickets WHERE id=$1`, uid(ticketID)).Scan(&account, &org, &creator, &requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TicketCustomer{}, nil
	}
	if err != nil {
		return TicketCustomer{}, err
	}
	out := TicketCustomer{AccountID: uuidText(account), OrgID: uuidText(org), RequestID: requestID}
	if out.AccountID == "" {
		out.AccountID = uuidText(creator)
	}
	return out, nil
}

// TicketOwner is TicketCustomer without the request id.
func (s *Service) TicketOwner(ctx context.Context, ticketID string) (accountID, orgID string, err error) {
	customer, err := s.TicketCustomer(ctx, ticketID)
	return customer.AccountID, customer.OrgID, err
}

func uuidText(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	value, _ := v.Value()
	out, _ := value.(string)
	return out
}
