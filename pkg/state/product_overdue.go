package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrProductInvoiceOverdue refuses a new Product Control start from a
// customer held for an overdue invoice (ADR-20261018212918). Work already
// admitted keeps running; reads stay available.
var ErrProductInvoiceOverdue = errors.New("state: Product Control customer has an overdue invoice")

// ProductInvoiceOverdueError names the invoice that holds the customer.
type ProductInvoiceOverdueError struct {
	DueAt            time.Time
	HostedInvoiceURL string
}

func (e *ProductInvoiceOverdueError) Error() string {
	return fmt.Sprintf("%v: due %s", ErrProductInvoiceOverdue, e.DueAt.UTC().Format(time.DateOnly))
}

func (e *ProductInvoiceOverdueError) Unwrap() error { return ErrProductInvoiceOverdue }

// ProductOverduePolicy is an app's rule for unpaid Product Control
// statements, kept outside the immutable catalog because it is collection
// policy for every customer at once.
type ProductOverduePolicy struct {
	AccountID             string
	AppID                 string
	BlockOverdueAfterDays int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ProductOverdueHold refuses one customer's new starts because of its oldest
// overdue statement. Since is when the hold was placed. It is separate from
// the owner's suspended flag; lifting one never lifts the other.
type ProductOverdueHold struct {
	AccountID        string
	AppID            string
	TenantID         string
	DocumentID       string
	ExportID         string
	DueAt            time.Time
	Since            time.Time
	HostedInvoiceURL string
}

// ProductOverdueStore persists Product Control overdue policies and holds.
type ProductOverdueStore interface {
	PutProductOverduePolicy(ctx context.Context, p ProductOverduePolicy) (ProductOverduePolicy, error)
	// GetProductOverduePolicy is ErrNotFound when the app has none.
	GetProductOverduePolicy(ctx context.Context, accountID, appID string) (ProductOverduePolicy, error)
	// DeleteProductOverduePolicy removes the policy and lifts its holds.
	DeleteProductOverduePolicy(ctx context.Context, accountID, appID string) (lifted []ProductOverdueHold, err error)
	// SyncProductOverdueHolds holds every customer whose oldest exported
	// statement is overdue at now under its app's policy and lifts every
	// other hold. It is idempotent; placed and lifted report what changed.
	// A held invoice's next payment sync is brought within
	// api.ProductOverdueHeldSyncInterval.
	SyncProductOverdueHolds(ctx context.Context, now time.Time) (placed, lifted []ProductOverdueHold, err error)
	// ListProductOverdueHolds and GetProductOverdueHold return holds that
	// apply: their policy exists and their invoice is still unpaid.
	ListProductOverdueHolds(ctx context.Context, accountID, appID string) ([]ProductOverdueHold, error)
	// GetProductOverdueHold is ErrNotFound when the customer is not held.
	GetProductOverdueHold(ctx context.Context, accountID, appID, tenantID string) (ProductOverdueHold, error)
}

var (
	_ ProductOverdueStore = (*MemStore)(nil)
	_ ProductOverdueStore = (*PgStore)(nil)
)

func validateProductOverduePolicy(p ProductOverduePolicy) error {
	if err := validateProductOverdueApp(p.AccountID, p.AppID); err != nil {
		return err
	}
	if p.BlockOverdueAfterDays < 0 || p.BlockOverdueAfterDays > api.ProductOverdueBlockMaxDays {
		return fmt.Errorf("%w: block_overdue_after_days must be 0 to %d", ErrInvalidArgument, api.ProductOverdueBlockMaxDays)
	}
	return nil
}

func validateProductOverdueApp(accountID, appID string) error {
	if _, err := uuid.Parse(accountID); err != nil {
		return fmt.Errorf("%w: account_id must be a UUID", ErrInvalidArgument)
	}
	if _, err := uuid.Parse(appID); err != nil {
		return fmt.Errorf("%w: app_id must be a UUID", ErrInvalidArgument)
	}
	return nil
}

// productInvoiceUnpaid reports whether a held invoice still holds: open or
// uncollectible with an amount left.
func productInvoiceUnpaid(e StatementInvoiceExport) bool {
	return (e.PaymentStatus == InvoicePaymentOpen || e.PaymentStatus == InvoicePaymentUncollectible) &&
		(e.AmountRemainingMinor == nil || *e.AmountRemainingMinor > 0)
}

func productOverdueRefusal(h ProductOverdueHold) error {
	return &ProductInvoiceOverdueError{DueAt: h.DueAt, HostedInvoiceURL: h.HostedInvoiceURL}
}

func sortProductOverdueHolds(holds []ProductOverdueHold) {
	sort.Slice(holds, func(i, j int) bool {
		if !holds[i].DueAt.Equal(holds[j].DueAt) {
			return holds[i].DueAt.Before(holds[j].DueAt)
		}
		return holds[i].TenantID < holds[j].TenantID
	})
}

// --- MemStore ---

func (m *MemStore) PutProductOverduePolicy(_ context.Context, p ProductOverduePolicy) (ProductOverduePolicy, error) {
	if err := validateProductOverduePolicy(p); err != nil {
		return ProductOverduePolicy{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[p.AppID]; !ok || app.AccountID != p.AccountID {
		return ProductOverduePolicy{}, ErrNotFound
	}
	d := m.productLocked()
	if d.overduePolicies == nil {
		d.overduePolicies = map[string]ProductOverduePolicy{}
	}
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	if prior, ok := d.overduePolicies[p.AppID]; ok {
		p.CreatedAt = prior.CreatedAt
	}
	d.overduePolicies[p.AppID] = p
	return p, nil
}

func (m *MemStore) GetProductOverduePolicy(_ context.Context, accountID, appID string) (ProductOverduePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.productLocked().overduePolicies[appID]
	if !ok || p.AccountID != accountID {
		return ProductOverduePolicy{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) DeleteProductOverduePolicy(_ context.Context, accountID, appID string) ([]ProductOverdueHold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.productLocked()
	if p, ok := d.overduePolicies[appID]; !ok || p.AccountID != accountID {
		return nil, ErrNotFound
	}
	delete(d.overduePolicies, appID)
	lifted := []ProductOverdueHold{}
	for key, hold := range d.overdueHolds {
		if hold.AccountID == accountID && hold.AppID == appID {
			lifted = append(lifted, hold)
			delete(d.overdueHolds, key)
		}
	}
	sortProductOverdueHolds(lifted)
	return lifted, nil
}

func (m *MemStore) SyncProductOverdueHolds(_ context.Context, now time.Time) ([]ProductOverdueHold, []ProductOverdueHold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.productLocked()
	due := map[string]ProductOverdueHold{}
	for _, e := range m.statementInvoiceExports {
		if e.SourceKind != StatementSourceProductControl {
			continue
		}
		doc, ok := d.documents[e.StatementID]
		if !ok || doc.account != e.AccountID || doc.document.Kind != "statement" {
			continue
		}
		policy, ok := d.overduePolicies[doc.app]
		if !ok || policy.AccountID != doc.account || !overdueExport(e, policy.BlockOverdueAfterDays, now) {
			continue
		}
		key := doc.app + "/" + doc.document.TenantID
		if prior, ok := due[key]; ok && (prior.DueAt.Before(*e.DueAt) || prior.DueAt.Equal(*e.DueAt) && prior.ExportID < e.ID) {
			continue
		}
		due[key] = ProductOverdueHold{AccountID: doc.account, AppID: doc.app, TenantID: doc.document.TenantID, DocumentID: doc.document.ID,
			ExportID: e.ID, DueAt: e.DueAt.UTC(), Since: now.UTC()}
	}
	if d.overdueHolds == nil {
		d.overdueHolds = map[string]ProductOverdueHold{}
	}
	placed, lifted := []ProductOverdueHold{}, []ProductOverdueHold{}
	for key, hold := range d.overdueHolds {
		if _, still := due[key]; !still {
			lifted = append(lifted, hold)
			delete(d.overdueHolds, key)
		}
	}
	soon := now.Add(api.ProductOverdueHeldSyncInterval).UTC()
	for key, hold := range due {
		if prior, ok := d.overdueHolds[key]; ok {
			hold.Since = prior.Since
		} else {
			placed = append(placed, hold)
		}
		d.overdueHolds[key] = hold
		if e := m.statementInvoiceExports[hold.ExportID]; e.NextStatusSyncAt != nil && e.NextStatusSyncAt.After(soon) {
			e.NextStatusSyncAt = &soon
			m.statementInvoiceExports[hold.ExportID] = e
		}
	}
	sortProductOverdueHolds(placed)
	sortProductOverdueHolds(lifted)
	return placed, lifted, nil
}

func (m *MemStore) ListProductOverdueHolds(_ context.Context, accountID, appID string) ([]ProductOverdueHold, error) {
	if err := validateProductOverdueApp(accountID, appID); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ProductOverdueHold{}
	for _, hold := range m.productLocked().overdueHolds {
		if hold.AccountID != accountID || hold.AppID != appID {
			continue
		}
		if hold, ok := m.productOverdueHoldLocked(accountID, appID, hold.TenantID); ok {
			out = append(out, hold)
		}
	}
	sortProductOverdueHolds(out)
	return out, nil
}

func (m *MemStore) GetProductOverdueHold(_ context.Context, accountID, appID, tenantID string) (ProductOverdueHold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hold, ok := m.productOverdueHoldLocked(accountID, appID, tenantID)
	if !ok {
		return ProductOverdueHold{}, ErrNotFound
	}
	return hold, nil
}

// productOverdueHoldLocked is a customer's hold while its policy exists and
// its invoice is still unpaid.
func (m *MemStore) productOverdueHoldLocked(accountID, appID, tenantID string) (ProductOverdueHold, bool) {
	d := m.productLocked()
	hold, ok := d.overdueHolds[appID+"/"+tenantID]
	if !ok || hold.AccountID != accountID {
		return ProductOverdueHold{}, false
	}
	if p, ok := d.overduePolicies[appID]; !ok || p.AccountID != accountID {
		return ProductOverdueHold{}, false
	}
	e, ok := m.statementInvoiceExports[hold.ExportID]
	if !ok || !productInvoiceUnpaid(e) {
		return ProductOverdueHold{}, false
	}
	hold.HostedInvoiceURL = e.HostedInvoiceURL
	return hold, true
}

// --- PgStore ---

func productOverduePolicyFromPG(row sqlc.ProductControlOverduePolicy) ProductOverduePolicy {
	return ProductOverduePolicy{AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), BlockOverdueAfterDays: int(row.BlockOverdueAfterDays),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC()}
}

func productOverdueHoldFromPG(row sqlc.ProductControlOverdueHold, hostedURL string) ProductOverdueHold {
	return ProductOverdueHold{AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), TenantID: pgUUIDString(row.TenantID),
		DocumentID: pgUUIDString(row.DocumentID), ExportID: pgUUIDString(row.ExportID), DueAt: row.DueAt.Time.UTC(), Since: row.Since.Time.UTC(),
		HostedInvoiceURL: hostedURL}
}

func (s *PgStore) PutProductOverduePolicy(ctx context.Context, p ProductOverduePolicy) (ProductOverduePolicy, error) {
	if err := validateProductOverduePolicy(p); err != nil {
		return ProductOverduePolicy{}, err
	}
	row, err := sqlc.New().ProductOverduePutPolicy(ctx, s.pool, sqlc.ProductOverduePutPolicyParams{Days: int32(p.BlockOverdueAfterDays),
		AccountID: mustPgUUID(p.AccountID), AppID: mustPgUUID(p.AppID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductOverduePolicy{}, ErrNotFound
	}
	if err != nil {
		return ProductOverduePolicy{}, fmt.Errorf("put product overdue policy: %w", err)
	}
	return productOverduePolicyFromPG(row), nil
}

func (s *PgStore) GetProductOverduePolicy(ctx context.Context, accountID, appID string) (ProductOverduePolicy, error) {
	if err := validateProductOverdueApp(accountID, appID); err != nil {
		return ProductOverduePolicy{}, err
	}
	row, err := sqlc.New().ProductOverduePolicy(ctx, s.pool, sqlc.ProductOverduePolicyParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductOverduePolicy{}, ErrNotFound
	}
	if err != nil {
		return ProductOverduePolicy{}, fmt.Errorf("get product overdue policy: %w", err)
	}
	return productOverduePolicyFromPG(row), nil
}

// DeleteProductOverduePolicy waits for a running recomputation, so a hold it
// lifts cannot be placed again by that recomputation.
func (s *PgStore) DeleteProductOverduePolicy(ctx context.Context, accountID, appID string) ([]ProductOverdueHold, error) {
	if err := validateProductOverdueApp(accountID, appID); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.ProductOverdueLock(ctx, tx); err != nil {
		return nil, fmt.Errorf("lock product overdue holds: %w", err)
	}
	deleted, err := q.ProductOverdueDeletePolicy(ctx, tx, sqlc.ProductOverdueDeletePolicyParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return nil, fmt.Errorf("delete product overdue policy: %w", err)
	}
	if deleted == 0 {
		return nil, ErrNotFound
	}
	rows, err := q.ProductOverdueDeleteAppHolds(ctx, tx, sqlc.ProductOverdueDeleteAppHoldsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return nil, fmt.Errorf("lift product overdue holds: %w", err)
	}
	lifted := make([]ProductOverdueHold, 0, len(rows))
	for _, row := range rows {
		lifted = append(lifted, productOverdueHoldFromPG(row, ""))
	}
	sortProductOverdueHolds(lifted)
	return lifted, tx.Commit(ctx)
}

// SyncProductOverdueHolds recomputes every hold in one transaction under an
// advisory lock, so apid replicas never report a change twice.
func (s *PgStore) SyncProductOverdueHolds(ctx context.Context, now time.Time) ([]ProductOverdueHold, []ProductOverdueHold, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if locked, err := q.ProductOverdueTryLock(ctx, tx); err != nil || !locked {
		return nil, nil, err
	}
	dueRows, err := q.ProductOverdueDue(ctx, tx, pgTime(now))
	if err != nil {
		return nil, nil, fmt.Errorf("find overdue product statements: %w", err)
	}
	current, err := q.ProductOverdueAllHolds(ctx, tx)
	if err != nil {
		return nil, nil, fmt.Errorf("load product overdue holds: %w", err)
	}
	held := map[string]sqlc.ProductControlOverdueHold{}
	for _, row := range current {
		held[pgUUIDString(row.AppID)+"/"+pgUUIDString(row.TenantID)] = row
	}
	placed, lifted := []ProductOverdueHold{}, []ProductOverdueHold{}
	for _, d := range dueRows {
		key := pgUUIDString(d.AppID) + "/" + pgUUIDString(d.TenantID)
		row := sqlc.ProductControlOverdueHold{AppID: d.AppID, TenantID: d.TenantID, AccountID: d.AccountID, DocumentID: d.DocumentID,
			ExportID: d.ExportID, DueAt: d.DueAt, Since: pgTime(now)}
		prior, ok := held[key]
		delete(held, key)
		if ok {
			row.Since = prior.Since
			if prior.ExportID == row.ExportID && prior.DocumentID == row.DocumentID && prior.DueAt.Time.Equal(row.DueAt.Time) {
				continue
			}
		}
		if err := q.ProductOverdueUpsertHold(ctx, tx, sqlc.ProductOverdueUpsertHoldParams(row)); err != nil {
			return nil, nil, fmt.Errorf("place product overdue hold: %w", err)
		}
		if !ok {
			placed = append(placed, productOverdueHoldFromPG(row, ""))
		}
	}
	for _, row := range held {
		if err := q.ProductOverdueDeleteHold(ctx, tx, sqlc.ProductOverdueDeleteHoldParams{AppID: row.AppID, TenantID: row.TenantID}); err != nil {
			return nil, nil, fmt.Errorf("lift product overdue hold: %w", err)
		}
		lifted = append(lifted, productOverdueHoldFromPG(row, ""))
	}
	if err := q.ProductOverdueResyncHeld(ctx, tx, pgTime(now.Add(api.ProductOverdueHeldSyncInterval))); err != nil {
		return nil, nil, fmt.Errorf("resync held product invoices: %w", err)
	}
	sortProductOverdueHolds(placed)
	sortProductOverdueHolds(lifted)
	return placed, lifted, tx.Commit(ctx)
}

func (s *PgStore) ListProductOverdueHolds(ctx context.Context, accountID, appID string) ([]ProductOverdueHold, error) {
	if err := validateProductOverdueApp(accountID, appID); err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ProductOverdueHolds(ctx, s.pool, sqlc.ProductOverdueHoldsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return nil, fmt.Errorf("list product overdue holds: %w", err)
	}
	out := make([]ProductOverdueHold, 0, len(rows))
	for _, row := range rows {
		out = append(out, productOverdueHoldFromPG(sqlc.ProductControlOverdueHold{AppID: row.AppID, TenantID: row.TenantID, AccountID: row.AccountID,
			DocumentID: row.DocumentID, ExportID: row.ExportID, DueAt: row.DueAt, Since: row.Since}, row.HostedInvoiceUrl))
	}
	return out, nil
}

func (s *PgStore) GetProductOverdueHold(ctx context.Context, accountID, appID, tenantID string) (ProductOverdueHold, error) {
	return productOverdueHoldPG(ctx, s.pool, accountID, appID, tenantID)
}

// productOverdueHoldPG reads a customer's applying hold; admission calls it
// inside its transaction.
func productOverdueHoldPG(ctx context.Context, db sqlc.DBTX, accountID, appID, tenantID string) (ProductOverdueHold, error) {
	if err := validateProductOverdueApp(accountID, appID); err != nil {
		return ProductOverdueHold{}, err
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return ProductOverdueHold{}, fmt.Errorf("%w: tenant_id must be a UUID", ErrInvalidArgument)
	}
	row, err := sqlc.New().ProductOverdueCustomerHold(ctx, db, sqlc.ProductOverdueCustomerHoldParams{AccountID: mustPgUUID(accountID),
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductOverdueHold{}, ErrNotFound
	}
	if err != nil {
		return ProductOverdueHold{}, fmt.Errorf("get product overdue hold: %w", err)
	}
	return productOverdueHoldFromPG(sqlc.ProductControlOverdueHold{AppID: row.AppID, TenantID: row.TenantID, AccountID: row.AccountID,
		DocumentID: row.DocumentID, ExportID: row.ExportID, DueAt: row.DueAt, Since: row.Since}, row.HostedInvoiceUrl), nil
}
