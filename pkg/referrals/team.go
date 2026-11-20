package referrals

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
)

// Sales roles, strongest first. Owner admins come only from configuration.
const (
	RoleAdmin   = "admin"
	RoleManager = "manager"
	RoleRep     = "rep"
)

// RoleRank orders roles so the strongest grant wins; unknown roles rank 0.
func RoleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleManager:
		return 2
	case RoleRep:
		return 1
	}
	return 0
}

var (
	ErrNotSalesCredit    = errors.New("referral is not sales credit")
	ErrPayoutRecorded    = errors.New("a payout is already recorded for this customer")
	ErrPayoutNotEligible = errors.New("commission is not eligible for payout")
	ErrSameOwner         = errors.New("credit already belongs to that salesperson")
)

// TeamMember is a portal-managed or config-managed member, with the facts an
// owner admin needs to see why someone does or does not have access.
type TeamMember struct {
	AccountID     string     `json:"account_id"`
	Email         string     `json:"email"`
	Role          string     `json:"role"`
	ConfigRole    string     `json:"config_role,omitempty"`
	StoredRole    string     `json:"stored_role,omitempty"`
	StoredActive  bool       `json:"stored_active"`
	Version       int64      `json:"version"`
	AccountStatus string     `json:"account_status"`
	EmailVerified bool       `json:"email_verified"`
	MFAEnrolled   bool       `json:"mfa_enrolled"`
	Ready         bool       `json:"ready"`
	CommissionBPS *int32     `json:"commission_bps"`
	CommissionAt  *time.Time `json:"commission_effective_from,omitempty"`
}

type Agreement struct {
	ID             string    `json:"id"`
	CommissionBPS  int32     `json:"commission_bps"`
	EffectiveFrom  time.Time `json:"effective_from"`
	Note           string    `json:"note"`
	CreatedAt      time.Time `json:"created_at"`
	CreatedByEmail string    `json:"created_by_email,omitempty"`
}

type AgreementInput struct {
	CommissionBPS int32     `json:"commission_bps"`
	EffectiveFrom time.Time `json:"-"`
	Note          string    `json:"note"`
}

type CreditChange struct {
	ID                string    `json:"id"`
	FromOwnerID       string    `json:"from_owner_id,omitempty"`
	ToOwnerID         string    `json:"to_owner_id"`
	FromEmail         string    `json:"from_email,omitempty"`
	ToEmail           string    `json:"to_email,omitempty"`
	FromCommissionBPS int32     `json:"from_commission_bps"`
	ToCommissionBPS   int32     `json:"to_commission_bps"`
	Reason            string    `json:"reason"`
	ChangedByEmail    string    `json:"changed_by_email,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type PayoutInput struct {
	AmountCents int64  `json:"amount_cents"`
	PaidOn      string `json:"paid_on"`
	Reference   string `json:"reference"`
	Note        string `json:"note"`
}

type Payout struct {
	ID              string     `json:"id"`
	AccountID       string     `json:"account_id"`
	OwnerID         string     `json:"owner_id"`
	OwnerEmail      string     `json:"owner_email"`
	CustomerEmail   string     `json:"customer_email,omitempty"`
	CustomerOrg     string     `json:"customer_org,omitempty"`
	AmountCents     int64      `json:"amount_cents"`
	Currency        string     `json:"currency"`
	PaidOn          string     `json:"paid_on"`
	Reference       string     `json:"reference"`
	Note            string     `json:"note"`
	RecordedByEmail string     `json:"recorded_by_email,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	VoidedAt        *time.Time `json:"voided_at,omitempty"`
	VoidReason      string     `json:"void_reason,omitempty"`
}

// TeamStore holds portal-managed membership, agreements, credit moves and the
// payout ledger. Callers enforce roles; the store enforces row invariants.
type TeamStore interface {
	TeamRoster(context.Context) (map[string]string, error)
	TeamMembers(context.Context, []string) ([]TeamMember, error)
	AccountIDByEmail(context.Context, string) (string, error)
	SaveTeamMember(ctx context.Context, actor, account, role string, active bool, version int64) error
	AddAgreement(ctx context.Context, actor, account string, in AgreementInput) (Agreement, error)
	Agreements(context.Context, string) ([]Agreement, error)
	CurrentCommission(ctx context.Context, account string, fallback int32) (int32, error)
	MoveCredit(ctx context.Context, actor, customer, toOwner, reason string, fallbackBPS int32) (CreditChange, error)
	CreditHistory(context.Context, string) ([]CreditChange, error)
	RecordPayout(ctx context.Context, actor, customer string, in PayoutInput) (Payout, error)
	VoidPayout(ctx context.Context, actor, id, reason string) (bool, error)
	Payouts(ctx context.Context, scope Scope, owner string, includeVoided bool) ([]Payout, error)
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	at := t.Time
	return &at
}

func (s *PostgresStore) TeamRoster(ctx context.Context) (map[string]string, error) {
	rows, err := s.q.SalesTeamRoster(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("read sales roster: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Email] = r.Role
	}
	return out, nil
}

// TeamMembers lists portal members plus accounts named by configuration.
// Role and Ready are filled in by the caller, which knows the config grants.
func (s *PostgresStore) TeamMembers(ctx context.Context, configEmails []string) ([]TeamMember, error) {
	rows, err := s.q.ListSalesTeamMembers(ctx, s.pool, configEmails)
	if err != nil {
		return nil, fmt.Errorf("list sales team: %w", err)
	}
	out := make([]TeamMember, 0, len(rows))
	for _, r := range rows {
		m := TeamMember{AccountID: idString(r.ID), Email: r.Email, StoredRole: r.StoredRole, StoredActive: r.StoredActive, Version: r.Version,
			AccountStatus: r.Status, EmailVerified: r.Verified, MFAEnrolled: r.MfaEnrolled, CommissionAt: timePtr(r.AgreementFrom)}
		if r.AgreementBps >= 0 {
			bps := r.AgreementBps
			m.CommissionBPS = &bps
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *PostgresStore) AccountIDByEmail(ctx context.Context, email string) (string, error) {
	row, err := s.q.SalesAccountByEmail(ctx, s.pool, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find sales account: %w", err)
	}
	return idString(row.ID), nil
}

// SaveTeamMember inserts a member when version is 0, else updates the row at
// that version. A stale version or an existing row on insert is ErrConflict.
func (s *PostgresStore) SaveTeamMember(ctx context.Context, actor, account, role string, active bool, version int64) error {
	var n int64
	var err error
	if version == 0 {
		n, err = s.q.InsertSalesTeamMember(ctx, s.pool, sqlc.InsertSalesTeamMemberParams{AccountID: pgID(account), Role: role, UpdatedBy: pgID(actor)})
	} else {
		n, err = s.q.UpdateSalesTeamMember(ctx, s.pool, sqlc.UpdateSalesTeamMemberParams{Role: role, Active: active, UpdatedBy: pgID(actor), AccountID: pgID(account), ExpectedVersion: version})
	}
	if err != nil {
		return fmt.Errorf("save sales team member: %w", err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) AddAgreement(ctx context.Context, actor, account string, in AgreementInput) (Agreement, error) {
	id := uuid.New()
	at := in.EffectiveFrom.UTC()
	err := s.q.InsertCommissionAgreement(ctx, s.pool, sqlc.InsertCommissionAgreementParams{ID: pgtype.UUID{Bytes: id, Valid: true}, AccountID: pgID(account),
		CommissionBps: in.CommissionBPS, EffectiveFrom: pgtype.Timestamptz{Time: at, Valid: true}, Note: in.Note, CreatedBy: pgID(actor)})
	if err != nil {
		return Agreement{}, fmt.Errorf("save commission agreement: %w", err)
	}
	return Agreement{ID: id.String(), CommissionBPS: in.CommissionBPS, EffectiveFrom: at, Note: in.Note, CreatedAt: time.Now().UTC()}, nil
}

func (s *PostgresStore) Agreements(ctx context.Context, account string) ([]Agreement, error) {
	rows, err := s.q.ListCommissionAgreements(ctx, s.pool, pgID(account))
	if err != nil {
		return nil, fmt.Errorf("list commission agreements: %w", err)
	}
	out := make([]Agreement, 0, len(rows))
	for _, r := range rows {
		out = append(out, Agreement{ID: idString(r.ID), CommissionBPS: r.CommissionBps, EffectiveFrom: r.EffectiveFrom.Time, Note: r.Note, CreatedAt: r.CreatedAt.Time, CreatedByEmail: r.CreatedByEmail})
	}
	return out, nil
}

func (s *PostgresStore) currentCommission(ctx context.Context, db sqlc.DBTX, account string, fallback int32) (int32, error) {
	bps, err := s.q.CurrentCommissionBps(ctx, db, pgID(account))
	if errors.Is(err, pgx.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read commission agreement: %w", err)
	}
	return bps, nil
}

func (s *PostgresStore) CurrentCommission(ctx context.Context, account string, fallback int32) (int32, error) {
	return s.currentCommission(ctx, s.pool, account, fallback)
}

// MoveCredit reassigns one sales attribution to another salesperson at that
// person's current agreement. Paid history stays with whoever was paid, so a
// customer with a live payout cannot move. The caller checks the target is an
// eligible salesperson.
func (s *PostgresStore) MoveCredit(ctx context.Context, actor, customer, toOwner, reason string, fallbackBPS int32) (CreditChange, error) {
	if _, err := s.EnsureLink(ctx, toOwner, "sales"); err != nil {
		return CreditChange{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreditChange{}, fmt.Errorf("begin credit move: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.LockSalesCredit(ctx, tx, pgID(customer))
	if errors.Is(err, pgx.ErrNoRows) {
		return CreditChange{}, ErrNotFound
	}
	if err != nil {
		return CreditChange{}, fmt.Errorf("lock sales credit: %w", err)
	}
	switch {
	case row.Kind != "sales":
		return CreditChange{}, ErrNotSalesCredit
	case row.HasPayout:
		return CreditChange{}, ErrPayoutRecorded
	case idString(row.OwnerID) == toOwner:
		return CreditChange{}, ErrSameOwner
	}
	link, err := s.q.OwnLink(ctx, tx, sqlc.OwnLinkParams{OwnerID: pgID(toOwner), Kind: "sales"})
	if err != nil {
		return CreditChange{}, fmt.Errorf("read target sales code: %w", err)
	}
	bps, err := s.currentCommission(ctx, tx, toOwner, fallbackBPS)
	if err != nil {
		return CreditChange{}, err
	}
	if _, err = s.q.MoveSalesCredit(ctx, tx, sqlc.MoveSalesCreditParams{Code: link.Code, CommissionBps: bps, UpdatedBy: pgID(actor), AccountID: pgID(customer)}); err != nil {
		return CreditChange{}, fmt.Errorf("move sales credit: %w", err)
	}
	id := uuid.New()
	change := CreditChange{ID: id.String(), FromOwnerID: idString(row.OwnerID), ToOwnerID: toOwner, FromCommissionBPS: row.CommissionBps, ToCommissionBPS: bps, Reason: reason, CreatedAt: time.Now().UTC()}
	if err = s.q.InsertCreditChange(ctx, tx, sqlc.InsertCreditChangeParams{ID: pgtype.UUID{Bytes: id, Valid: true}, AccountID: pgID(customer), FromOwnerID: row.OwnerID,
		ToOwnerID: pgID(toOwner), FromCommissionBps: row.CommissionBps, ToCommissionBps: bps, Reason: reason, ChangedBy: pgID(actor)}); err != nil {
		return CreditChange{}, fmt.Errorf("record credit move: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return CreditChange{}, fmt.Errorf("commit credit move: %w", err)
	}
	return change, nil
}

func (s *PostgresStore) CreditHistory(ctx context.Context, customer string) ([]CreditChange, error) {
	rows, err := s.q.ListCreditChanges(ctx, s.pool, pgID(customer))
	if err != nil {
		return nil, fmt.Errorf("list credit moves: %w", err)
	}
	out := make([]CreditChange, 0, len(rows))
	for _, r := range rows {
		c := CreditChange{ID: idString(r.ID), ToOwnerID: idString(r.ToOwnerID), FromEmail: r.FromEmail, ToEmail: r.ToEmail, FromCommissionBPS: r.FromCommissionBps,
			ToCommissionBPS: r.ToCommissionBps, Reason: r.Reason, ChangedByEmail: r.ChangedByEmail, CreatedAt: r.CreatedAt.Time}
		if r.FromOwnerID.Valid {
			c.FromOwnerID = idString(r.FromOwnerID)
		}
		out = append(out, c)
	}
	return out, nil
}

// RecordPayout books one commission payment made outside Gregale. The
// customer must be eligible sales credit with no live payout; the payee and
// currency come from the attribution, not the request.
func (s *PostgresStore) RecordPayout(ctx context.Context, actor, customer string, in PayoutInput) (Payout, error) {
	paid := dateValue(in.PaidOn)
	if !paid.Valid {
		return Payout{}, fmt.Errorf("invalid payout date")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Payout{}, fmt.Errorf("begin payout: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lock, err := s.q.LockSalesCredit(ctx, tx, pgID(customer))
	if errors.Is(err, pgx.ErrNoRows) {
		return Payout{}, ErrNotFound
	}
	if err != nil {
		return Payout{}, fmt.Errorf("lock sales credit: %w", err)
	}
	if lock.Kind != "sales" {
		return Payout{}, ErrNotSalesCredit
	}
	if lock.HasPayout {
		return Payout{}, ErrPayoutRecorded
	}
	candidate, err := s.q.PayoutCandidate(ctx, tx, pgID(customer))
	if err != nil {
		return Payout{}, fmt.Errorf("read payout candidate: %w", err)
	}
	if candidate.RewardStatus != "eligible" {
		return Payout{}, ErrPayoutNotEligible
	}
	id := uuid.New()
	err = s.q.InsertPayout(ctx, tx, sqlc.InsertPayoutParams{ID: pgtype.UUID{Bytes: id, Valid: true}, AccountID: pgID(customer), OwnerID: candidate.OwnerID,
		AmountCents: in.AmountCents, Currency: candidate.Currency, PaidOn: paid, Reference: in.Reference, Note: in.Note, RecordedBy: pgID(actor)})
	var conflict *pgconn.PgError
	if errors.As(err, &conflict) && conflict.Code == "23505" {
		return Payout{}, ErrPayoutRecorded
	}
	if err != nil {
		return Payout{}, fmt.Errorf("record payout: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Payout{}, fmt.Errorf("commit payout: %w", err)
	}
	return Payout{ID: id.String(), AccountID: customer, OwnerID: idString(candidate.OwnerID), AmountCents: in.AmountCents, Currency: candidate.Currency,
		PaidOn: in.PaidOn, Reference: in.Reference, Note: in.Note, CreatedAt: time.Now().UTC()}, nil
}

func (s *PostgresStore) VoidPayout(ctx context.Context, actor, id, reason string) (bool, error) {
	n, err := s.q.VoidPayout(ctx, s.pool, sqlc.VoidPayoutParams{VoidedBy: pgID(actor), VoidReason: reason, ID: pgID(id)})
	if err != nil {
		return false, fmt.Errorf("void payout: %w", err)
	}
	return n > 0, nil
}

func (s *PostgresStore) Payouts(ctx context.Context, scope Scope, owner string, includeVoided bool) ([]Payout, error) {
	rows, err := s.q.ListPayouts(ctx, s.pool, sqlc.ListPayoutsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: owner, IncludeVoided: includeVoided})
	if err != nil {
		return nil, fmt.Errorf("list payouts: %w", err)
	}
	out := make([]Payout, 0, len(rows))
	for _, r := range rows {
		out = append(out, Payout{ID: idString(r.ID), AccountID: idString(r.AccountID), OwnerID: idString(r.OwnerID), OwnerEmail: r.OwnerEmail, CustomerEmail: r.CustomerEmail,
			CustomerOrg: r.CustomerOrg, AmountCents: r.AmountCents, Currency: r.Currency, PaidOn: r.PaidOn.Time.Format(time.DateOnly), Reference: r.Reference, Note: r.Note,
			RecordedByEmail: r.RecordedByEmail, CreatedAt: r.CreatedAt.Time, VoidedAt: timePtr(r.VoidedAt), VoidReason: r.VoidReason})
	}
	return out, nil
}
