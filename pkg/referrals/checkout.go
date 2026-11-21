package referrals

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
)

var (
	ErrCheckoutIneligible  = errors.New("referral requires a first paid purchase")
	ErrAlreadyAttributed   = errors.New("referral attribution already exists")
	ErrInvalidCheckoutCode = errors.New("salesperson code is unavailable")
)

type CheckoutState struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
	Code     string `json:"code,omitempty"`
}

type CheckoutStore interface {
	CheckoutState(context.Context, string) (CheckoutState, error)
	OrgCheckoutState(context.Context, string) (CheckoutState, error)
	AttributeCheckout(context.Context, string, Attribution, []string) (bool, error)
}

// LockBillingAccount is shared by attribution and the invoice writer. It must
// precede their reads/writes, in a transaction, with a fresh read after the lock.
// No provider request runs while this lock is held.
func LockBillingAccount(ctx context.Context, tx sqlc.DBTX, account string) error {
	if err := sqlc.New().LockReferralBillingAccount(ctx, tx, pgID(account)); err != nil {
		return fmt.Errorf("lock referral billing account: %w", err)
	}
	return nil
}

// Subject kinds: a personal account or a company organization.
const (
	SubjectAccount = "account"
	SubjectOrg     = "org"
)

// checkoutRow is the first-purchase state shared by both subject kinds.
type checkoutRow struct {
	Plan, Status, Subscription, Code, Kind string
	Attributed, Paid, Excluded             bool
}

func checkoutState(row checkoutRow) CheckoutState {
	s := CheckoutState{Eligible: true}
	switch {
	case row.Attributed:
		s.Reason, s.Code = "already_attributed", row.Code
	case row.Excluded:
		s.Reason = "unavailable"
	case row.Paid || row.Plan != "free" || row.Subscription != "":
		s.Reason = "not_first_purchase"
	case row.Status != "active":
		s.Reason = "unavailable"
	}
	s.Eligible = s.Reason == ""
	return s
}

func (s *PostgresStore) readCheckout(ctx context.Context, db sqlc.DBTX, subject, kind string) (checkoutRow, error) {
	var out checkoutRow
	if kind == SubjectOrg {
		row, err := s.q.OrgCheckoutReferralState(ctx, db, pgID(subject))
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, fmt.Errorf("read organization checkout referral: %w", err)
		}
		return checkoutRow{Plan: row.Plan, Status: row.Status, Subscription: row.Subscription, Code: row.Code, Kind: row.Kind, Attributed: row.Attributed, Paid: row.Paid, Excluded: row.Excluded}, nil
	}
	row, err := s.q.CheckoutReferralState(ctx, db, pgID(subject))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("read checkout referral: %w", err)
	}
	return checkoutRow{Plan: row.Plan, Status: row.Status, Subscription: row.Subscription, Code: row.Code, Kind: row.Kind, Attributed: row.Attributed, Paid: row.Paid}, nil
}

func (s *PostgresStore) CheckoutState(ctx context.Context, account string) (CheckoutState, error) {
	row, err := s.readCheckout(ctx, s.pool, account, SubjectAccount)
	if err != nil {
		return CheckoutState{}, err
	}
	return checkoutState(row), nil
}

// OrgCheckoutState is CheckoutState for a company organization's first purchase.
func (s *PostgresStore) OrgCheckoutState(ctx context.Context, org string) (CheckoutState, error) {
	row, err := s.readCheckout(ctx, s.pool, org, SubjectOrg)
	if err != nil {
		return CheckoutState{}, err
	}
	return checkoutState(row), nil
}

// AttributeCheckout records a customer's explicit choice before checkout. The
// first choice and captured terms survive retries, abandoned checkouts and
// provider failures. Only the authenticated invoice writer records a conversion.
func (s *PostgresStore) AttributeCheckout(ctx context.Context, account string, input Attribution, members []string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin checkout referral: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = LockBillingAccount(ctx, tx, account); err != nil {
		return false, err
	}
	created, err := s.attributeCheckout(ctx, tx, account, input, members)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit checkout referral: %w", err)
	}
	return created, nil
}

func (s *PostgresStore) attributeCheckout(ctx context.Context, tx pgx.Tx, account string, input Attribution, members []string) (bool, error) {
	subject := input.SubjectKind
	if subject == "" {
		subject = SubjectAccount
	}
	row, err := s.readCheckout(ctx, tx, account, subject)
	if err != nil {
		return false, err
	}
	if row.Attributed {
		if row.Kind == "sales" && row.Code == input.Code {
			return false, nil
		}
		return false, ErrAlreadyAttributed
	}
	if !checkoutState(row).Eligible {
		return false, ErrCheckoutIneligible
	}
	p := input.Policy
	n, err := s.q.AttributeCheckout(ctx, tx, sqlc.AttributeCheckoutParams{
		AccountID: pgID(account), Code: input.Code, MemberEmails: members, Currency: p.Currency,
		CustomerRewardCents: p.CustomerRewardCents, ReferredRewardCents: p.ReferredRewardCents,
		CommissionBps: p.CommissionBPS, HoldDays: p.HoldDays, ConversionDays: p.ConversionDays,
		SubjectKind: subject, SelectedBy: pgID(input.SelectedBy),
	})
	if err != nil {
		return false, fmt.Errorf("attribute checkout: %w", err)
	}
	if n == 0 {
		return false, ErrInvalidCheckoutCode
	}
	return true, nil
}
