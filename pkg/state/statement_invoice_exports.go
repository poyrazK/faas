package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// Statement invoice export constants (ADR-1502, ADR-1507).
const (
	BillingProviderStripe = "stripe"
	BillingProviderPaddle = "paddle"
	BillingProviderLago   = "lago"

	StatementSourceAppConsumer    = "app_consumer"
	StatementSourcePlatformTenant = "platform_tenant"

	StatementInvoiceExportCreating  = "creating"
	StatementInvoiceExportCompleted = "completed"
)

// Collection modes (ADR-1311): what the provider does with an exported
// invoice. None leaves a draft for the operator (ADR-1502); send has the
// provider issue and email it; charge has the provider charge the
// customer's saved payment method. Gregale never holds the money.
const (
	CollectionNone   = "none"
	CollectionSend   = "send"
	CollectionCharge = "charge"
)

// DefaultCollectionDaysUntilDue is when a sent invoice falls due.
const DefaultCollectionDaysUntilDue = 30

// ProviderCollectionModes are the modes each provider supports: Paddle
// issues an invoice with the customer's address but cannot charge a saved
// payment method for one (ADR-20261019100333), and Lago sends invoices by its
// own email settings, so it can only add charging.
var ProviderCollectionModes = map[string][]string{
	BillingProviderStripe: {CollectionNone, CollectionSend, CollectionCharge},
	BillingProviderPaddle: {CollectionNone, CollectionSend},
	BillingProviderLago:   {CollectionNone, CollectionCharge},
}

// ValidCollection reports whether provider supports mode.
func ValidCollection(provider, mode string) bool {
	for _, m := range ProviderCollectionModes[provider] {
		if m == mode {
			return true
		}
	}
	return false
}

// BillingProviders are the providers statements export to (ADR-1507).
var BillingProviders = []string{BillingProviderStripe, BillingProviderPaddle, BillingProviderLago}

// ValidBillingProvider reports whether p is a known export provider.
func ValidBillingProvider(p string) bool {
	return p == BillingProviderStripe || p == BillingProviderPaddle || p == BillingProviderLago
}

// BillingConnection is an account's sealed payment-provider credential.
// SealedSecret never leaves the server; KeyLast4 and Livemode identify it.
// APIURL is set for Lago only (ADR-1507).
type BillingConnection struct {
	AccountID    string
	Provider     string
	SealedSecret []byte
	KeyLast4     string
	Livemode     bool
	APIURL       string
	// Collection is the connection's collection mode (ADR-1311), and
	// DaysUntilDue when a sent invoice falls due.
	Collection   string
	DaysUntilDue int
	// AutomaticTax has Stripe calculate tax on exported invoices (ADR-1313).
	AutomaticTax bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StatementInvoiceExport is one draft provider invoice for one finalized
// statement. It is recorded before any provider call; ProviderInvoiceID is
// set once the invoice exists, and Completed once every item is on it and
// the statement's handoff names it.
type StatementInvoiceExport struct {
	ID                string
	AccountID         string
	SourceKind        string
	StatementID       string
	Provider          string
	Customer          string
	Status            string
	ProviderInvoiceID string
	Currency          string
	AmountMinor       int64
	Livemode          bool
	// Collection and DaysUntilDue are the connection's collection mode when
	// the export began (ADR-1311), so a resumed export keeps it.
	Collection   string
	DaysUntilDue int
	// AutomaticTax is the connection's Stripe tax setting when the export
	// began (ADR-1313).
	AutomaticTax bool
	CreatedAt    time.Time
	CompletedAt  *time.Time
	// Payment fields mirror the provider invoice (ADR-1504); PaymentStatus is
	// empty until the first sync, and NextStatusSyncAt is nil once final.
	PaymentStatus        string
	AmountPaidMinor      *int64
	AmountRemainingMinor *int64
	DueAt                *time.Time
	PaidAt               *time.Time
	HostedInvoiceURL     string
	StatusSyncedAt       *time.Time
	NextStatusSyncAt     *time.Time
	OverdueNotifiedAt    *time.Time
	// Attempt counts retargets after a rejection; RejectedAt is set while
	// the provider's refusal before creating anything is the export's last
	// outcome (ADR-20261019180000).
	Attempt    int
	RejectedAt *time.Time
	// HandoffReserved is set once the export reserved its statement's
	// handoff, before calling the provider: an overlapping statement then
	// cannot be handed off or exported (ADR-20261020211500).
	HandoffReserved bool

	// claimToken and claimedUntil hold the MemStore's claim.
	claimToken   string
	claimedUntil time.Time
}

// ErrStatementInvoiceExportInProgress reports that another attempt holds
// the export's claim (ADR-20261019180000).
var ErrStatementInvoiceExportInProgress = errors.New("state: statement invoice export in progress")

// Retargetable reports whether the next attempt may change the export's
// provider or customer: the provider refused it before any invoice existed.
func (e StatementInvoiceExport) Retargetable() bool {
	return e.Status != StatementInvoiceExportCompleted && e.ProviderInvoiceID == "" && e.RejectedAt != nil
}

// Overdue reports whether an open invoice is past due with an amount left.
func (e StatementInvoiceExport) Overdue(now time.Time) bool {
	return e.PaymentStatus == InvoicePaymentOpen && e.DueAt != nil && e.DueAt.Before(now) &&
		(e.AmountRemainingMinor == nil || *e.AmountRemainingMinor > 0)
}

// StatementInvoiceExportStore persists billing connections and exports.
type StatementInvoiceExportStore interface {
	PutBillingConnection(context.Context, BillingConnection) (BillingConnection, error)
	GetBillingConnection(ctx context.Context, accountID, provider string) (BillingConnection, error)
	DeleteBillingConnection(ctx context.Context, accountID, provider string) error
	// BeginStatementInvoiceExport records an export or returns the existing
	// one for the statement; existing reports which. An existing export for
	// a different customer or provider is ErrConflict, unless it is
	// Retargetable and unclaimed: then it takes e's target and settings as
	// its next attempt (ADR-20261019180000).
	BeginStatementInvoiceExport(context.Context, StatementInvoiceExport) (export StatementInvoiceExport, existing bool, err error)
	// ClaimStatementInvoiceExport holds the export for one attempt, named by
	// token, until until, and returns it as it stands;
	// ErrStatementInvoiceExportInProgress while another attempt's claim
	// holds (ADR-20261019180000).
	ClaimStatementInvoiceExport(ctx context.Context, accountID, exportID, token string, until time.Time) (StatementInvoiceExport, error)
	// ReleaseStatementInvoiceExport ends the attempt's claim.
	ReleaseStatementInvoiceExport(ctx context.Context, accountID, exportID, token string) error
	// RejectStatementInvoiceExport records that the provider refused the
	// attempt before creating anything, ends its claim and releases its
	// handoff reservation.
	RejectStatementInvoiceExport(ctx context.Context, accountID, exportID, token string) error
	// ReserveStatementHandoff reserves the export's statement handoff under
	// ADR-238's overlap rules, as creating the handoff would; ErrConflict
	// when another statement's handoff or reservation overlaps it
	// (ADR-20261020211500).
	ReserveStatementHandoff(ctx context.Context, accountID, exportID string) error
	// SetStatementInvoiceExportInvoice records the provider invoice once;
	// recording a different invoice is ErrConflict.
	SetStatementInvoiceExportInvoice(ctx context.Context, accountID, exportID, invoiceID string) error
	CompleteStatementInvoiceExport(ctx context.Context, accountID, exportID string) (StatementInvoiceExport, error)
	GetStatementInvoiceExport(ctx context.Context, accountID, sourceKind, statementID string) (StatementInvoiceExport, error)
}

var (
	_ StatementInvoiceExportStore = (*MemStore)(nil)
	_ StatementInvoiceExportStore = (*PgStore)(nil)
)

func validateStatementInvoiceExport(e StatementInvoiceExport) error {
	for name, value := range map[string]string{"account_id": e.AccountID, "statement_id": e.StatementID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%w: %s must be a UUID", ErrInvalidArgument, name)
		}
	}
	if e.SourceKind != StatementSourceAppConsumer && e.SourceKind != StatementSourcePlatformTenant {
		return fmt.Errorf("%w: unknown statement source %q", ErrInvalidArgument, e.SourceKind)
	}
	if !ValidBillingProvider(e.Provider) || e.Customer == "" || !isUpperASCIICurrency(e.Currency) ||
		!ValidCollection(e.Provider, e.Collection) || e.DaysUntilDue < 1 || e.DaysUntilDue > 365 ||
		(e.AutomaticTax && e.Provider != BillingProviderStripe) {
		return fmt.Errorf("%w: invalid statement invoice export", ErrInvalidArgument)
	}
	return nil
}

// --- MemStore ---

func validateBillingConnection(c BillingConnection) error {
	if !ValidBillingProvider(c.Provider) || len(c.KeyLast4) != 4 || len(c.SealedSecret) == 0 ||
		(c.APIURL != "" && (len(c.APIURL) > 512 || !strings.HasPrefix(c.APIURL, "https://"))) ||
		!ValidCollection(c.Provider, c.Collection) || c.DaysUntilDue < 1 || c.DaysUntilDue > 365 ||
		(c.AutomaticTax && c.Provider != BillingProviderStripe) {
		return ErrInvalidArgument
	}
	return nil
}

// normalizeCollection defaults an unset collection mode to none, due in
// DefaultCollectionDaysUntilDue days.
func normalizeCollection(mode string, days int) (string, int) {
	if mode == "" {
		mode = CollectionNone
	}
	if days == 0 {
		days = DefaultCollectionDaysUntilDue
	}
	return mode, days
}

func (m *MemStore) PutBillingConnection(_ context.Context, c BillingConnection) (BillingConnection, error) {
	c.Collection, c.DaysUntilDue = normalizeCollection(c.Collection, c.DaysUntilDue)
	if err := validateBillingConnection(c); err != nil {
		return BillingConnection{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.billingConnections == nil {
		m.billingConnections = map[string]BillingConnection{}
	}
	now := time.Now().UTC()
	key := c.AccountID + "/" + c.Provider
	if prior, ok := m.billingConnections[key]; ok {
		c.CreatedAt = prior.CreatedAt
	} else {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	c.SealedSecret = append([]byte(nil), c.SealedSecret...)
	m.billingConnections[key] = c
	return c, nil
}

func (m *MemStore) GetBillingConnection(_ context.Context, accountID, provider string) (BillingConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.billingConnections[accountID+"/"+provider]
	if !ok {
		return BillingConnection{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) DeleteBillingConnection(_ context.Context, accountID, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "/" + provider
	if _, ok := m.billingConnections[key]; !ok {
		return ErrNotFound
	}
	delete(m.billingConnections, key)
	return nil
}

func (m *MemStore) BeginStatementInvoiceExport(_ context.Context, e StatementInvoiceExport) (StatementInvoiceExport, bool, error) {
	e.Collection, e.DaysUntilDue = normalizeCollection(e.Collection, e.DaysUntilDue)
	if err := validateStatementInvoiceExport(e); err != nil {
		return StatementInvoiceExport{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, existing := range m.statementInvoiceExports {
		if existing.SourceKind == e.SourceKind && existing.StatementID == e.StatementID {
			if existing.AccountID == e.AccountID && existing.Retargetable() && !existing.claimedUntil.After(now) {
				existing.Provider, existing.Customer, existing.Currency, existing.AmountMinor = e.Provider, e.Customer, e.Currency, e.AmountMinor
				existing.Livemode, existing.Collection, existing.DaysUntilDue, existing.AutomaticTax = e.Livemode, e.Collection, e.DaysUntilDue, e.AutomaticTax
				existing.Attempt, existing.RejectedAt = existing.Attempt+1, nil
				m.statementInvoiceExports[id] = existing
				return existing, true, nil
			}
			if existing.AccountID != e.AccountID || existing.Customer != e.Customer || existing.Provider != e.Provider {
				return StatementInvoiceExport{}, true, ErrConflict
			}
			return existing, true, nil
		}
	}
	if m.statementInvoiceExports == nil {
		m.statementInvoiceExports = map[string]StatementInvoiceExport{}
	}
	e.ID, e.Status, e.CreatedAt, e.ProviderInvoiceID, e.CompletedAt = uuid.NewString(), StatementInvoiceExportCreating, time.Now().UTC(), "", nil
	m.statementInvoiceExports[e.ID] = e
	return e, false, nil
}

func (m *MemStore) SetStatementInvoiceExportInvoice(_ context.Context, accountID, exportID, invoiceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.statementInvoiceExports[exportID]
	if !ok || e.AccountID != accountID {
		return ErrNotFound
	}
	if e.ProviderInvoiceID != "" && e.ProviderInvoiceID != invoiceID {
		return ErrConflict
	}
	e.ProviderInvoiceID = invoiceID
	m.statementInvoiceExports[exportID] = e
	return nil
}

func (m *MemStore) CompleteStatementInvoiceExport(_ context.Context, accountID, exportID string) (StatementInvoiceExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.statementInvoiceExports[exportID]
	if !ok || e.AccountID != accountID {
		return StatementInvoiceExport{}, ErrNotFound
	}
	if e.ProviderInvoiceID == "" {
		return StatementInvoiceExport{}, ErrConflict
	}
	if e.Status != StatementInvoiceExportCompleted {
		now := time.Now().UTC()
		next := now.Add(api.StatementInvoiceFirstSyncDelay)
		e.Status, e.CompletedAt = StatementInvoiceExportCompleted, &now
		e.PaymentStatus, e.NextStatusSyncAt = InvoicePaymentDraft, &next
		e.claimToken, e.claimedUntil = "", time.Time{}
		m.statementInvoiceExports[exportID] = e
	}
	return e, nil
}

func (m *MemStore) ClaimStatementInvoiceExport(_ context.Context, accountID, exportID, token string, until time.Time) (StatementInvoiceExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.statementInvoiceExports[exportID]
	if !ok || e.AccountID != accountID {
		return StatementInvoiceExport{}, ErrNotFound
	}
	if e.claimToken != token && e.claimedUntil.After(time.Now()) {
		return StatementInvoiceExport{}, ErrStatementInvoiceExportInProgress
	}
	e.claimToken, e.claimedUntil = token, until
	m.statementInvoiceExports[exportID] = e
	return e, nil
}

func (m *MemStore) ReleaseStatementInvoiceExport(_ context.Context, accountID, exportID, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.statementInvoiceExports[exportID]
	if !ok || e.AccountID != accountID {
		return ErrNotFound
	}
	if e.claimToken == token {
		e.claimToken, e.claimedUntil = "", time.Time{}
		m.statementInvoiceExports[exportID] = e
	}
	return nil
}

func (m *MemStore) RejectStatementInvoiceExport(_ context.Context, accountID, exportID, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.statementInvoiceExports[exportID]
	if !ok || e.AccountID != accountID {
		return ErrNotFound
	}
	if e.claimToken != token || e.ProviderInvoiceID != "" {
		return ErrConflict
	}
	now := time.Now().UTC()
	e.RejectedAt, e.claimToken, e.claimedUntil, e.HandoffReserved = &now, "", time.Time{}, false
	m.statementInvoiceExports[exportID] = e
	return nil
}

func (m *MemStore) ReserveStatementHandoff(_ context.Context, accountID, exportID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.statementInvoiceExports[exportID]
	if !ok || e.AccountID != accountID {
		return ErrNotFound
	}
	if e.HandoffReserved {
		return nil
	}
	var blocked bool
	switch e.SourceKind {
	case StatementSourceAppConsumer:
		statement, ok := m.apiConsumerUsageStatements[e.StatementID]
		if !ok || statement.AccountID != accountID {
			return ErrNotFound
		}
		blocked = m.apiConsumerHandoffOverlapLocked(statement)
	case StatementSourcePlatformTenant:
		statement, ok := m.platformTenantStatements[e.StatementID]
		if !ok || statement.AccountID != accountID {
			return ErrNotFound
		}
		blocked = m.tenantHandoffOverlapLocked(statement)
	}
	if blocked {
		return ErrConflict
	}
	e.HandoffReserved = true
	m.statementInvoiceExports[exportID] = e
	return nil
}

func (m *MemStore) GetStatementInvoiceExport(_ context.Context, accountID, sourceKind, statementID string) (StatementInvoiceExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.statementInvoiceExports {
		if e.AccountID == accountID && e.SourceKind == sourceKind && e.StatementID == statementID {
			return e, nil
		}
	}
	return StatementInvoiceExport{}, ErrNotFound
}

// --- PgStore ---

func (s *PgStore) PutBillingConnection(ctx context.Context, c BillingConnection) (BillingConnection, error) {
	c.Collection, c.DaysUntilDue = normalizeCollection(c.Collection, c.DaysUntilDue)
	if err := validateBillingConnection(c); err != nil {
		return BillingConnection{}, err
	}
	err := s.pool.QueryRow(ctx, `insert into account_billing_connections
			(account_id, provider, sealed_secret, key_last4, livemode, api_url, collection, days_until_due, automatic_tax)
		values ($1::uuid, $2, $3, $4, $5, nullif($6, ''), $7, $8, $9)
		on conflict (account_id, provider) do update
		   set sealed_secret = excluded.sealed_secret, key_last4 = excluded.key_last4,
		       livemode = excluded.livemode, api_url = excluded.api_url, collection = excluded.collection,
		       days_until_due = excluded.days_until_due, automatic_tax = excluded.automatic_tax, updated_at = now()
		returning created_at, updated_at`,
		c.AccountID, c.Provider, c.SealedSecret, c.KeyLast4, c.Livemode, c.APIURL, c.Collection, c.DaysUntilDue, c.AutomaticTax).
		Scan(&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return BillingConnection{}, fmt.Errorf("put billing connection: %w", err)
	}
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, nil
}

func (s *PgStore) GetBillingConnection(ctx context.Context, accountID, provider string) (BillingConnection, error) {
	c := BillingConnection{AccountID: accountID, Provider: provider}
	err := s.pool.QueryRow(ctx, `select sealed_secret, key_last4, livemode, coalesce(api_url, ''), collection, days_until_due,
		automatic_tax, created_at, updated_at
		from account_billing_connections where account_id = $1::uuid and provider = $2`, accountID, provider).
		Scan(&c.SealedSecret, &c.KeyLast4, &c.Livemode, &c.APIURL, &c.Collection, &c.DaysUntilDue, &c.AutomaticTax, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingConnection{}, ErrNotFound
	}
	if err != nil {
		return BillingConnection{}, fmt.Errorf("get billing connection: %w", err)
	}
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, nil
}

func (s *PgStore) DeleteBillingConnection(ctx context.Context, accountID, provider string) error {
	tag, err := s.pool.Exec(ctx, `delete from account_billing_connections where account_id = $1::uuid and provider = $2`, accountID, provider)
	if err != nil {
		return fmt.Errorf("delete billing connection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const statementInvoiceExportCols = `id, account_id, source_kind, statement_id, provider, customer, status,
	coalesce(provider_invoice_id, ''), currency, amount_minor, livemode, created_at, completed_at,
	coalesce(payment_status, ''), amount_paid_minor, amount_remaining_minor, due_at, paid_at,
	coalesce(hosted_invoice_url, ''), status_synced_at, next_status_sync_at, overdue_notified_at, collection, days_until_due, automatic_tax,
	attempt, rejected_at, handoff_reserved`

func scanStatementInvoiceExport(row pgx.Row) (StatementInvoiceExport, error) {
	var e StatementInvoiceExport
	err := row.Scan(&e.ID, &e.AccountID, &e.SourceKind, &e.StatementID, &e.Provider, &e.Customer, &e.Status,
		&e.ProviderInvoiceID, &e.Currency, &e.AmountMinor, &e.Livemode, &e.CreatedAt, &e.CompletedAt,
		&e.PaymentStatus, &e.AmountPaidMinor, &e.AmountRemainingMinor, &e.DueAt, &e.PaidAt,
		&e.HostedInvoiceURL, &e.StatusSyncedAt, &e.NextStatusSyncAt, &e.OverdueNotifiedAt, &e.Collection, &e.DaysUntilDue, &e.AutomaticTax,
		&e.Attempt, &e.RejectedAt, &e.HandoffReserved)
	if errors.Is(err, pgx.ErrNoRows) {
		return StatementInvoiceExport{}, ErrNotFound
	}
	if err != nil {
		return StatementInvoiceExport{}, err
	}
	e.CreatedAt = e.CreatedAt.UTC()
	if e.CompletedAt != nil {
		at := e.CompletedAt.UTC()
		e.CompletedAt = &at
	}
	return e, nil
}

func (s *PgStore) BeginStatementInvoiceExport(ctx context.Context, e StatementInvoiceExport) (StatementInvoiceExport, bool, error) {
	e.Collection, e.DaysUntilDue = normalizeCollection(e.Collection, e.DaysUntilDue)
	if err := validateStatementInvoiceExport(e); err != nil {
		return StatementInvoiceExport{}, false, err
	}
	created, err := scanStatementInvoiceExport(s.pool.QueryRow(ctx, `insert into usage_statement_invoice_exports
			(account_id, source_kind, statement_id, provider, customer, currency, amount_minor, livemode, collection, days_until_due, automatic_tax)
		values ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
		on conflict (source_kind, statement_id) do nothing
		returning `+statementInvoiceExportCols,
		e.AccountID, e.SourceKind, e.StatementID, e.Provider, e.Customer, e.Currency, e.AmountMinor, e.Livemode, e.Collection, e.DaysUntilDue,
		e.AutomaticTax))
	if err == nil {
		return created, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return StatementInvoiceExport{}, false, fmt.Errorf("begin statement invoice export: %w", err)
	}
	// A rejected, unclaimed export takes the new target as its next attempt
	// (ADR-20261019180000).
	retargeted, err := scanStatementInvoiceExport(s.pool.QueryRow(ctx, `update usage_statement_invoice_exports
		   set provider = $4, customer = $5, currency = $6, amount_minor = $7, livemode = $8, collection = $9,
		       days_until_due = $10, automatic_tax = $11, attempt = attempt + 1, rejected_at = null
		 where source_kind = $1 and statement_id = $2::uuid and account_id = $3::uuid
		   and status <> 'completed' and provider_invoice_id is null and rejected_at is not null
		   and (claimed_until is null or claimed_until <= now())
		returning `+statementInvoiceExportCols,
		e.SourceKind, e.StatementID, e.AccountID, e.Provider, e.Customer, e.Currency, e.AmountMinor, e.Livemode, e.Collection,
		e.DaysUntilDue, e.AutomaticTax))
	if err == nil {
		return retargeted, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return StatementInvoiceExport{}, false, fmt.Errorf("retarget statement invoice export: %w", err)
	}
	existing, err := scanStatementInvoiceExport(s.pool.QueryRow(ctx, `select `+statementInvoiceExportCols+`
		from usage_statement_invoice_exports where source_kind = $1 and statement_id = $2::uuid`, e.SourceKind, e.StatementID))
	if err != nil {
		return StatementInvoiceExport{}, false, fmt.Errorf("read statement invoice export: %w", err)
	}
	if existing.AccountID != e.AccountID || existing.Customer != e.Customer || existing.Provider != e.Provider {
		return StatementInvoiceExport{}, true, ErrConflict
	}
	return existing, true, nil
}

func (s *PgStore) SetStatementInvoiceExportInvoice(ctx context.Context, accountID, exportID, invoiceID string) error {
	var current string
	err := s.pool.QueryRow(ctx, `update usage_statement_invoice_exports
		   set provider_invoice_id = coalesce(provider_invoice_id, $3)
		 where id = $1::uuid and account_id = $2::uuid
		returning provider_invoice_id`, exportID, accountID, invoiceID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("set statement invoice export invoice: %w", err)
	}
	if current != invoiceID {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) CompleteStatementInvoiceExport(ctx context.Context, accountID, exportID string) (StatementInvoiceExport, error) {
	e, err := scanStatementInvoiceExport(s.pool.QueryRow(ctx, `update usage_statement_invoice_exports
		   set status = 'completed', completed_at = coalesce(completed_at, now()),
		       payment_status = coalesce(payment_status, 'draft'), claim_token = null, claimed_until = null,
		       next_status_sync_at = case when payment_status is null then $3::timestamptz else next_status_sync_at end
		 where id = $1::uuid and account_id = $2::uuid and provider_invoice_id is not null
		returning `+statementInvoiceExportCols, exportID, accountID, time.Now().Add(api.StatementInvoiceFirstSyncDelay)))
	if errors.Is(err, ErrNotFound) {
		return StatementInvoiceExport{}, ErrConflict
	}
	if err != nil {
		return StatementInvoiceExport{}, fmt.Errorf("complete statement invoice export: %w", err)
	}
	return e, nil
}

func (s *PgStore) ClaimStatementInvoiceExport(ctx context.Context, accountID, exportID, token string, until time.Time) (StatementInvoiceExport, error) {
	e, err := scanStatementInvoiceExport(s.pool.QueryRow(ctx, `update usage_statement_invoice_exports
		   set claim_token = $3::uuid, claimed_until = $4
		 where id = $1::uuid and account_id = $2::uuid
		   and (claimed_until is null or claimed_until <= now() or claim_token = $3::uuid)
		returning `+statementInvoiceExportCols, exportID, accountID, token, until.UTC()))
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return StatementInvoiceExport{}, fmt.Errorf("claim statement invoice export: %w", err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `select exists (select 1 from usage_statement_invoice_exports where id = $1::uuid and account_id = $2::uuid)`,
		exportID, accountID).Scan(&exists); err != nil {
		return StatementInvoiceExport{}, fmt.Errorf("read statement invoice export claim: %w", err)
	}
	if exists {
		return StatementInvoiceExport{}, ErrStatementInvoiceExportInProgress
	}
	return StatementInvoiceExport{}, ErrNotFound
}

func (s *PgStore) ReleaseStatementInvoiceExport(ctx context.Context, accountID, exportID, token string) error {
	if _, err := s.pool.Exec(ctx, `update usage_statement_invoice_exports set claim_token = null, claimed_until = null
		where id = $1::uuid and account_id = $2::uuid and claim_token = $3::uuid`, exportID, accountID, token); err != nil {
		return fmt.Errorf("release statement invoice export: %w", err)
	}
	return nil
}

func (s *PgStore) RejectStatementInvoiceExport(ctx context.Context, accountID, exportID, token string) error {
	tag, err := s.pool.Exec(ctx, `update usage_statement_invoice_exports
		   set rejected_at = now(), claim_token = null, claimed_until = null, handoff_reserved = false
		 where id = $1::uuid and account_id = $2::uuid and claim_token = $3::uuid and provider_invoice_id is null`,
		exportID, accountID, token)
	if err != nil {
		return fmt.Errorf("reject statement invoice export: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ReserveStatementHandoff(ctx context.Context, accountID, exportID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin handoff reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Handoff creation takes the same account lock, so a reservation and a
	// handoff of overlapping statements cannot both pass the overlap check.
	var locked string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1::uuid for update`, accountID).Scan(&locked); err != nil {
		return fmt.Errorf("lock account for handoff reservation: %w", err)
	}
	var kind, statementID string
	var reserved bool
	err = tx.QueryRow(ctx, `select source_kind, statement_id::text, handoff_reserved from usage_statement_invoice_exports
		where id = $1::uuid and account_id = $2::uuid for update`, exportID, accountID).Scan(&kind, &statementID, &reserved)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read export for handoff reservation: %w", err)
	}
	if reserved {
		return nil
	}
	blocked, err := statementHandoffBlocked(ctx, tx, accountID, kind, statementID)
	if err != nil {
		return err
	}
	if blocked {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `update usage_statement_invoice_exports set handoff_reserved = true where id = $1::uuid`, exportID); err != nil {
		return fmt.Errorf("reserve handoff: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit handoff reservation: %w", err)
	}
	return nil
}

func (s *PgStore) GetStatementInvoiceExport(ctx context.Context, accountID, sourceKind, statementID string) (StatementInvoiceExport, error) {
	return scanStatementInvoiceExport(s.pool.QueryRow(ctx, `select `+statementInvoiceExportCols+`
		from usage_statement_invoice_exports
		where account_id = $1::uuid and source_kind = $2 and statement_id = $3::uuid`, accountID, sourceKind, statementID))
}
