package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CreditDiscountState is where a credit discount is in its life
// (ADR-20261104212028).
type CreditDiscountState string

const (
	// CreditDiscountAttached is on the subscription, waiting for its next
	// order.
	CreditDiscountAttached CreditDiscountState = "attached"
	// CreditDiscountRedeemed was applied to an order; its amount was drawn
	// from the subject's credits.
	CreditDiscountRedeemed CreditDiscountState = "redeemed"
	// CreditDiscountDetached was taken off the subscription, or replaced,
	// before any order carried it.
	CreditDiscountDetached CreditDiscountState = "detached"
)

// CreditDiscount is a one-time fixed provider discount Gregale attached to a
// billing subject's subscription to apply its account credit to the next
// order (ADR-20261104212028).
type CreditDiscount struct {
	ID                     string
	SubjectID              string
	Provider               string
	ProviderSubscriptionID string
	ProviderDiscountID     string
	AmountCents            int64
	Currency               string
	State                  CreditDiscountState
	// ProviderInvoiceID and RedeemedCents are set once an order carried it.
	ProviderInvoiceID string
	RedeemedCents     int64
	CreatedAt         time.Time
}

// CreditDiscountStore keeps the credit discounts apid attached
// (ADR-20261104212028). apid is the only writer.
type CreditDiscountStore interface {
	// CreateCreditDiscount records a discount just attached.
	CreateCreditDiscount(ctx context.Context, d CreditDiscount) (CreditDiscount, error)
	// AttachedCreditDiscounts lists subject's attached discounts, newest first.
	AttachedCreditDiscounts(ctx context.Context, subjectID, provider string) ([]CreditDiscount, error)
	// CreditDiscountByProviderID finds a discount by its provider id, or
	// returns ErrNotFound when Gregale did not create it.
	CreditDiscountByProviderID(ctx context.Context, provider, providerDiscountID string) (CreditDiscount, error)
	// RedeemCreditDiscount marks the discount redeemed by an order. It
	// reports false when it was already redeemed.
	RedeemCreditDiscount(ctx context.Context, id, providerInvoiceID string, redeemedCents int64) (bool, error)
	// DetachCreditDiscount marks an attached discount detached.
	DetachCreditDiscount(ctx context.Context, id string) error
	// CreditDiscountSubjects lists the subjects with credit left or a
	// discount still attached with provider.
	CreditDiscountSubjects(ctx context.Context, provider string) ([]string, error)
}

var (
	_ CreditDiscountStore = (*PgStore)(nil)
	_ CreditDiscountStore = (*MemStore)(nil)
)

// CreateCreditDiscount records a discount just attached.
func (s *PgStore) CreateCreditDiscount(ctx context.Context, d CreditDiscount) (CreditDiscount, error) {
	subject, err := parsePgUUID(d.SubjectID)
	if err != nil {
		return CreditDiscount{}, err
	}
	row, err := sqlc.New().CreateCreditDiscount(ctx, s.pool, sqlc.CreateCreditDiscountParams{
		SubjectID: subject, Provider: d.Provider, ProviderSubscriptionID: d.ProviderSubscriptionID,
		ProviderDiscountID: d.ProviderDiscountID, AmountCents: d.AmountCents, Currency: d.Currency,
	})
	if err != nil {
		return CreditDiscount{}, fmt.Errorf("state: create credit discount: %w", err)
	}
	d.ID, d.State, d.CreatedAt = row.ID, CreditDiscountAttached, row.CreatedAt.Time.UTC()
	return d, nil
}

// AttachedCreditDiscounts lists subject's attached discounts, newest first.
func (s *PgStore) AttachedCreditDiscounts(ctx context.Context, subjectID, provider string) ([]CreditDiscount, error) {
	subject, err := parsePgUUID(subjectID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().AttachedCreditDiscounts(ctx, s.pool, sqlc.AttachedCreditDiscountsParams{SubjectID: subject, Provider: provider})
	if err != nil {
		return nil, fmt.Errorf("state: list attached credit discounts: %w", err)
	}
	out := make([]CreditDiscount, 0, len(rows))
	for _, r := range rows {
		out = append(out, CreditDiscount{ID: r.ID, SubjectID: r.SubjectID, Provider: r.Provider,
			ProviderSubscriptionID: r.ProviderSubscriptionID, ProviderDiscountID: r.ProviderDiscountID,
			AmountCents: r.AmountCents, Currency: r.Currency, State: CreditDiscountState(r.State),
			ProviderInvoiceID: r.ProviderInvoiceID, RedeemedCents: r.RedeemedCents, CreatedAt: r.CreatedAt.Time.UTC()})
	}
	return out, nil
}

// CreditDiscountByProviderID finds a discount by its provider id.
func (s *PgStore) CreditDiscountByProviderID(ctx context.Context, provider, providerDiscountID string) (CreditDiscount, error) {
	r, err := sqlc.New().CreditDiscountByProviderID(ctx, s.pool, sqlc.CreditDiscountByProviderIDParams{
		Provider: provider, ProviderDiscountID: providerDiscountID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CreditDiscount{}, ErrNotFound
	}
	if err != nil {
		return CreditDiscount{}, fmt.Errorf("state: read credit discount: %w", err)
	}
	return CreditDiscount{ID: r.ID, SubjectID: r.SubjectID, Provider: r.Provider,
		ProviderSubscriptionID: r.ProviderSubscriptionID, ProviderDiscountID: r.ProviderDiscountID,
		AmountCents: r.AmountCents, Currency: r.Currency, State: CreditDiscountState(r.State),
		ProviderInvoiceID: r.ProviderInvoiceID, RedeemedCents: r.RedeemedCents, CreatedAt: r.CreatedAt.Time.UTC()}, nil
}

// RedeemCreditDiscount marks the discount redeemed by an order.
func (s *PgStore) RedeemCreditDiscount(ctx context.Context, id, providerInvoiceID string, redeemedCents int64) (bool, error) {
	pgID, err := parsePgUUID(id)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New().RedeemCreditDiscount(ctx, s.pool, sqlc.RedeemCreditDiscountParams{
		ID: pgID, ProviderInvoiceID: providerInvoiceID, RedeemedCents: redeemedCents,
	})
	if err != nil {
		return false, fmt.Errorf("state: redeem credit discount: %w", err)
	}
	return n > 0, nil
}

// DetachCreditDiscount marks an attached discount detached.
func (s *PgStore) DetachCreditDiscount(ctx context.Context, id string) error {
	pgID, err := parsePgUUID(id)
	if err != nil {
		return err
	}
	if _, err := sqlc.New().DetachCreditDiscount(ctx, s.pool, pgID); err != nil {
		return fmt.Errorf("state: detach credit discount: %w", err)
	}
	return nil
}

// CreditDiscountSubjects lists the subjects with credit left or a discount
// still attached.
func (s *PgStore) CreditDiscountSubjects(ctx context.Context, provider string) ([]string, error) {
	ids, err := sqlc.New().CreditDiscountSubjects(ctx, s.pool, provider)
	if err != nil {
		return nil, fmt.Errorf("state: list credit discount subjects: %w", err)
	}
	return ids, nil
}

// CreateCreditDiscount records a discount just attached.
func (m *MemStore) CreateCreditDiscount(_ context.Context, d CreditDiscount) (CreditDiscount, error) {
	if d.AmountCents <= 0 || d.ProviderDiscountID == "" || d.ProviderSubscriptionID == "" {
		return CreditDiscount{}, fmt.Errorf("state: create credit discount: %w", ErrConflict)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.creditDiscounts == nil {
		m.creditDiscounts = map[string]CreditDiscount{}
	}
	for _, existing := range m.creditDiscounts {
		if existing.Provider == d.Provider && existing.ProviderDiscountID == d.ProviderDiscountID {
			return CreditDiscount{}, fmt.Errorf("state: create credit discount: %w", ErrConflict)
		}
	}
	d.ID, d.State, d.CreatedAt = uuid.NewString(), CreditDiscountAttached, m.clock().UTC()
	d.ProviderInvoiceID, d.RedeemedCents = "", 0
	m.creditDiscounts[d.ID] = d
	return d, nil
}

// AttachedCreditDiscounts lists subject's attached discounts, newest first.
func (m *MemStore) AttachedCreditDiscounts(_ context.Context, subjectID, provider string) ([]CreditDiscount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CreditDiscount
	for _, d := range m.creditDiscounts {
		if d.SubjectID == subjectID && d.Provider == provider && d.State == CreditDiscountAttached {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// CreditDiscountByProviderID finds a discount by its provider id.
func (m *MemStore) CreditDiscountByProviderID(_ context.Context, provider, providerDiscountID string) (CreditDiscount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.creditDiscounts {
		if d.Provider == provider && d.ProviderDiscountID == providerDiscountID {
			return d, nil
		}
	}
	return CreditDiscount{}, ErrNotFound
}

// RedeemCreditDiscount marks the discount redeemed by an order.
func (m *MemStore) RedeemCreditDiscount(_ context.Context, id, providerInvoiceID string, redeemedCents int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.creditDiscounts[id]
	if !ok || d.State == CreditDiscountRedeemed {
		return false, nil
	}
	d.State, d.ProviderInvoiceID, d.RedeemedCents = CreditDiscountRedeemed, providerInvoiceID, redeemedCents
	m.creditDiscounts[id] = d
	return true, nil
}

// DetachCreditDiscount marks an attached discount detached.
func (m *MemStore) DetachCreditDiscount(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.creditDiscounts[id]; ok && d.State == CreditDiscountAttached {
		d.State = CreditDiscountDetached
		m.creditDiscounts[id] = d
	}
	return nil
}

// CreditDiscountSubjects lists the subjects with credit left or a discount
// still attached.
func (m *MemStore) CreditDiscountSubjects(_ context.Context, provider string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	seen := map[string]bool{}
	for _, c := range m.accountCredits {
		if c.CentsRemaining > 0 && (c.ExpiresAt == nil || c.ExpiresAt.After(now)) {
			seen[c.AccountID] = true
		}
	}
	for _, d := range m.creditDiscounts {
		if d.Provider == provider && d.State == CreditDiscountAttached {
			seen[d.SubjectID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
