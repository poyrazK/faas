package referrals

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
)

type PostgresStore struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, q: sqlc.New()}
}

func pgID(id string) pgtype.UUID {
	parsed, err := uuid.Parse(id)
	return pgtype.UUID{Bytes: parsed, Valid: err == nil}
}

func linkFrom(row sqlc.ReferralLink, err error) (Link, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return Link{Code: row.Code, OwnerID: uuid.UUID(row.OwnerID.Bytes).String(), Kind: row.Kind, Active: row.Active, CreatedAt: row.CreatedAt.Time}, err
}

func (s *PostgresStore) EnsureLink(ctx context.Context, owner, kind string) (Link, error) {
	return linkFrom(s.q.EnsureLink(ctx, s.pool, sqlc.EnsureLinkParams{Code: NewCode(), OwnerID: pgID(owner), Kind: kind}))
}

func (s *PostgresStore) Link(ctx context.Context, code string) (Link, error) {
	return linkFrom(s.q.GetLink(ctx, s.pool, code))
}

func (s *PostgresStore) OwnLink(ctx context.Context, owner, kind string) (Link, error) {
	return linkFrom(s.q.OwnLink(ctx, s.pool, sqlc.OwnLinkParams{OwnerID: pgID(owner), Kind: kind}))
}

func (s *PostgresStore) SetLinkActive(ctx context.Context, scope Scope, code string, active bool) (bool, error) {
	n, err := s.q.SetLinkActive(ctx, s.pool, sqlc.SetLinkActiveParams{Active: active, Code: code, OwnerID: pgID(scope.OwnerID), AllOwners: scope.All})
	return n > 0, err
}

// Attribute runs in the account-creation transaction. No existing-account
// endpoint can claim or change attribution; retries cannot replace its owner.
func Attribute(ctx context.Context, tx sqlc.DBTX, account string, input Attribution) error {
	p := input.Policy
	_, err := sqlc.New().AttributeSignup(ctx, tx, sqlc.AttributeSignupParams{AccountID: pgID(account), Code: input.Code, Currency: p.Currency,
		CustomerRewardCents: p.CustomerRewardCents, ReferredRewardCents: p.ReferredRewardCents, CommissionBps: p.CommissionBPS, HoldDays: p.HoldDays, ConversionDays: p.ConversionDays})
	if err != nil {
		return fmt.Errorf("attribute signup: %w", err)
	}
	return nil
}

// RecordPayment is called only by the authenticated invoice snapshot writer,
// inside its transaction. Replays preserve the first payment and hold start.
func RecordPayment(ctx context.Context, tx sqlc.DBTX, invoice string) error {
	if err := sqlc.New().RecordReferralPayment(ctx, tx, pgID(invoice)); err != nil {
		return fmt.Errorf("record referral payment: %w", err)
	}
	return nil
}

func (s *PostgresStore) List(ctx context.Context, scope Scope, after string, limit int32) ([]Record, error) {
	if after == "" {
		after = uuid.Nil.String()
	}
	rows, err := s.q.ListReferrals(ctx, s.pool, sqlc.ListReferralsParams{OwnerID: pgID(scope.OwnerID), AllOwners: scope.All, Kind: scope.Kind, AfterID: pgID(after), RowLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list referrals: %w", err)
	}
	out := make([]Record, 0, len(rows))
	for _, r := range rows {
		record := Record{AccountID: uuid.UUID(r.AccountID.Bytes).String(), OwnerID: uuid.UUID(r.OwnerID.Bytes).String(), Kind: r.Kind, Email: r.Email, Plan: r.Plan, AccountStatus: r.AccountStatus,
			CreatedAt: r.CreatedAt.Time, Stage: r.Stage, Note: r.Note, Apps: r.Apps, Paid: r.Paid, RewardStatus: r.RewardStatus, Currency: r.Currency, RewardCents: r.RewardCents, ReferredRewardCents: r.ReferredRewardCents, PaidCents: r.PaidCents}
		if r.FollowUp.Valid {
			record.FollowUp = r.FollowUp.Time.Format(time.DateOnly)
		}
		if r.EligibleAt.Valid {
			at := r.EligibleAt.Time
			record.EligibleAt = &at
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *PostgresStore) Summary(ctx context.Context, scope Scope) ([]Summary, error) {
	rows, err := s.q.ReferralSummary(ctx, s.pool, sqlc.ReferralSummaryParams{OwnerID: pgID(scope.OwnerID), AllOwners: scope.All, Kind: scope.Kind})
	if err != nil {
		return nil, fmt.Errorf("summarize referrals: %w", err)
	}
	out := make([]Summary, 0, len(rows))
	for _, r := range rows {
		out = append(out, Summary{Kind: r.Kind, Currency: r.Currency, Signups: r.Signups, Paid: r.Paid, Eligible: r.Eligible, RewardCents: r.RewardCents, PaidCents: r.PaidCents})
	}
	return out, nil
}

func (s *PostgresStore) UpdateLead(ctx context.Context, scope Scope, actor, account string, update LeadUpdate) (bool, error) {
	var follow pgtype.Date
	if update.FollowUp != "" {
		at, err := time.Parse(time.DateOnly, update.FollowUp)
		if err != nil {
			return false, err
		}
		follow = pgtype.Date{Time: at, Valid: true}
	}
	n, err := s.q.UpdateLead(ctx, s.pool, sqlc.UpdateLeadParams{Stage: update.Stage, Note: update.Note, FollowUp: follow, UpdatedBy: pgID(actor), AccountID: pgID(account), OwnerID: pgID(scope.OwnerID), AllOwners: scope.All})
	return n > 0, err
}
