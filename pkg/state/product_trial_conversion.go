package state

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrProductPaymentMethodRequired refuses a new start, or a self-service
// change, that needs a payment method on file (ADR-20261022180921).
var ErrProductPaymentMethodRequired = errors.New("state: Product Control plan needs a payment method on file")

// ProductTrialResolution is one ended trial the resolution step settled.
// FallbackPlan is set when the customer moved to it, effective TrialEndsAt;
// otherwise they kept Plan.
type ProductTrialResolution struct {
	AccountID, AppID, TenantID string
	Plan                       string
	RequiresPaymentMethod      bool
	FallbackPlan               string
	TrialEndsAt                time.Time
}

// ProductTrialConversionStore records a customer's payment method on file
// and resolves ended trials (ADR-20261022180921).
type ProductTrialConversionStore interface {
	// SetProductCustomerPaymentMethod records that a payment method is on
	// file, keeping when it first was, or clears it.
	SetProductCustomerPaymentMethod(ctx context.Context, account, app, tenant string, onFile bool) (api.ProductCustomer, error)
	// ResolveProductTrials resolves at most a batch of trials ended by now,
	// oldest first. A customer whose plan requires a payment method, who has
	// none and kept the plan since the trial ended moves to its fallback
	// plan effective at the trial's end.
	ResolveProductTrials(ctx context.Context, now time.Time) ([]ProductTrialResolution, error)
}

var (
	_ ProductTrialConversionStore = (*MemStore)(nil)
	_ ProductTrialConversionStore = (*PgStore)(nil)
)

// productTrialFallback is the plan an ended trial moves to, or "".
func productTrialFallback(r ProductTrialResolution, hasPaymentMethod, changedAfter bool, fallback string) string {
	if !r.RequiresPaymentMethod || hasPaymentMethod || changedAfter {
		return ""
	}
	return fallback
}

func (m *MemStore) SetProductCustomerPaymentMethod(_ context.Context, account, app, tenant string, onFile bool) (api.ProductCustomer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[app]
	if !ok || a.AccountID != account || a.Status == AppDeleted {
		return api.ProductCustomer{}, ErrNotFound
	}
	d := m.productLocked()
	customer, ok := d.customers[app+"/"+tenant]
	if !ok {
		return api.ProductCustomer{}, ErrNotFound
	}
	switch {
	case onFile && customer.PaymentMethodAt == nil:
		// Postgres keeps microseconds; both stores return what they keep.
		now := time.Now().UTC().Truncate(time.Microsecond)
		customer.PaymentMethodAt = &now
	case !onFile:
		customer.PaymentMethodAt = nil
	}
	d.customers[app+"/"+tenant] = customer
	return customer, nil
}

func (m *MemStore) ResolveProductTrials(_ context.Context, now time.Time) ([]ProductTrialResolution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.productLocked()
	type candidate struct {
		key      string
		customer api.ProductCustomer
	}
	var due []candidate
	for key, c := range d.customers {
		app := key[:strings.Index(key, "/")]
		if a, ok := m.apps[app]; ok && a.Status != AppDeleted && c.TrialEndsAt != nil && c.TrialResolvedAt == nil && !c.TrialEndsAt.After(now) {
			due = append(due, candidate{key, c})
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].customer.TrialEndsAt.Equal(*due[j].customer.TrialEndsAt) {
			return due[i].customer.TrialEndsAt.Before(*due[j].customer.TrialEndsAt)
		}
		return due[i].customer.TenantID < due[j].customer.TenantID
	})
	if len(due) > api.ProductControlTrialResolutionBatch {
		due = due[:api.ProductControlTrialResolutionBatch]
	}
	resolved := now.UTC().Truncate(time.Microsecond)
	out := []ProductTrialResolution{}
	for _, c := range due {
		app := c.key[:strings.Index(c.key, "/")]
		customer, ended := c.customer, *c.customer.TrialEndsAt
		plan := d.revisions[app+"/"+customer.Revision].Spec.Plans[customer.Plan]
		r := ProductTrialResolution{AccountID: m.apps[app].AccountID, AppID: app, TenantID: customer.TenantID, Plan: customer.Plan,
			RequiresPaymentMethod: plan.RequiresPaymentMethod, TrialEndsAt: ended}
		changedAfter := false
		for _, e := range d.planHistory[c.key] {
			changedAfter = changedAfter || e.at.After(ended)
		}
		if r.FallbackPlan = productTrialFallback(r, customer.PaymentMethodAt != nil, changedAfter, plan.FallbackPlan); r.FallbackPlan != "" {
			customer.Plan = r.FallbackPlan
			d.planHistory[c.key] = append(d.planHistory[c.key], productMemoryPlanEntry{account: r.AccountID, revision: customer.Revision, plan: r.FallbackPlan, at: ended})
		}
		customer.TrialResolvedAt = &resolved
		d.customers[c.key] = customer
		out = append(out, r)
	}
	return out, nil
}

func (s *PgStore) SetProductCustomerPaymentMethod(ctx context.Context, account, app, tenant string, onFile bool) (api.ProductCustomer, error) {
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	b, err := operationUUID(app)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	c, err := operationUUID(tenant)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	var out api.ProductCustomer
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		row, err := q.ProductControlCustomerForUpdate(ctx, tx, sqlc.ProductControlCustomerForUpdateParams{AccountID: a, AppID: b, TenantID: c})
		if err != nil {
			return mapErr(err)
		}
		out = withProductConversion(withProductTrial(api.ProductCustomer{TenantID: tenant, Revision: row.Revision, Plan: row.Plan, Suspended: row.Suspended,
			AuthOrganizationID: pgUUIDString(row.AuthOrganizationID)}, row.TrialStartsAt, row.TrialEndsAt), row.PaymentMethodAt, row.TrialResolvedAt)
		if onFile == (out.PaymentMethodAt != nil) {
			return nil
		}
		var at pgtype.Timestamptz
		if onFile {
			now := time.Now().UTC().Truncate(time.Microsecond)
			at, out.PaymentMethodAt = pgTime(now), &now
		} else {
			out.PaymentMethodAt = nil
		}
		return q.ProductControlSetPaymentMethod(ctx, tx, sqlc.ProductControlSetPaymentMethodParams{AccountID: a, AppID: b, TenantID: c, PaymentMethodAt: at})
	})
	return out, err
}

func (s *PgStore) ResolveProductTrials(ctx context.Context, now time.Time) ([]ProductTrialResolution, error) {
	resolved := now.UTC().Truncate(time.Microsecond)
	out := []ProductTrialResolution{}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		rows, err := q.ProductControlUnresolvedTrials(ctx, tx, sqlc.ProductControlUnresolvedTrialsParams{ResolvedAt: pgTime(resolved), PageLimit: int32(api.ProductControlTrialResolutionBatch)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			r := ProductTrialResolution{AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), TenantID: pgUUIDString(row.TenantID), Plan: row.Plan,
				RequiresPaymentMethod: row.RequiresPaymentMethod, TrialEndsAt: row.TrialEndsAt.Time.UTC()}
			var fallback pgtype.Text
			if r.FallbackPlan = productTrialFallback(r, row.PaymentMethodAt.Valid, row.ChangedAfterTrial, row.FallbackPlan); r.FallbackPlan != "" {
				fallback = pgtype.Text{String: r.FallbackPlan, Valid: true}
				if err := q.ProductControlAppendTrialFallback(ctx, tx, sqlc.ProductControlAppendTrialFallbackParams{AccountID: row.AccountID, AppID: row.AppID, TenantID: row.TenantID,
					Revision: row.Revision, Plan: r.FallbackPlan, EffectiveAt: row.TrialEndsAt}); err != nil {
					return err
				}
			}
			if err := q.ProductControlResolveTrial(ctx, tx, sqlc.ProductControlResolveTrialParams{AccountID: row.AccountID, AppID: row.AppID, TenantID: row.TenantID,
				ResolvedAt: pgTime(resolved), FallbackPlan: fallback}); err != nil {
				return err
			}
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// withProductConversion adds a customer's payment method on file and trial
// resolution (ADR-20261022180921).
func withProductConversion(c api.ProductCustomer, paymentMethod, resolved pgtype.Timestamptz) api.ProductCustomer {
	if paymentMethod.Valid {
		at := paymentMethod.Time.UTC()
		c.PaymentMethodAt = &at
	}
	if resolved.Valid {
		at := resolved.Time.UTC()
		c.TrialResolvedAt = &at
	}
	return c
}
