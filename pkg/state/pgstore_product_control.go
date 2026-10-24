package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func checkProductCustomerIdentityTx(ctx context.Context, tx pgx.Tx, def OperationDefinition, admission OperationAdmission) error {
	if admission.ProductAuthOrganizationID == "" && admission.ProductAuthApp == "" {
		return nil
	}
	customer, err := productCustomerTx(ctx, tx, admission.AccountID, def.AppID, admission.PlatformTenantID)
	if errors.Is(err, ErrNotFound) {
		return ErrProductAccess
	}
	if err != nil {
		return err
	}
	head, err := productCatalogTx(ctx, tx, admission.AccountID, def.AppID, "")
	if err != nil {
		return err
	}
	if customer.AuthOrganizationID == "" || customer.AuthOrganizationID != admission.ProductAuthOrganizationID || head.Spec.AuthApp == "" || head.Spec.AuthApp != admission.ProductAuthApp {
		return ErrProductAccess
	}
	return nil
}

func productCatalogTx(ctx context.Context, db sqlc.DBTX, account, appID, revision string) (api.ProductCatalog, error) {
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	q := sqlc.New()
	var spec []byte
	if revision == "" {
		row, err := q.ProductControlGetHead(ctx, db, sqlc.ProductControlGetHeadParams{AccountID: a, AppID: b})
		if err != nil {
			return api.ProductCatalog{}, mapErr(err)
		}
		revision, spec = row.Revision, row.Spec
	} else {
		spec, err = q.ProductControlGetRevision(ctx, db, sqlc.ProductControlGetRevisionParams{AccountID: a, AppID: b, Revision: revision})
		if err != nil {
			return api.ProductCatalog{}, mapErr(err)
		}
	}
	var decoded api.ProductCatalogSpec
	if err := json.Unmarshal(spec, &decoded); err != nil {
		return api.ProductCatalog{}, err
	}
	return api.ProductCatalog{Revision: revision, Spec: decoded}, nil
}

func productCustomerTx(ctx context.Context, db sqlc.DBTX, account, appID, tenant string) (api.ProductCustomer, error) {
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	c, err := operationUUID(tenant)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	row, err := sqlc.New().ProductControlGetCustomer(ctx, db, sqlc.ProductControlGetCustomerParams{AccountID: a, AppID: b, TenantID: c})
	if err != nil {
		return api.ProductCustomer{}, mapErr(err)
	}
	return withProductConversion(withProductTrial(api.ProductCustomer{TenantID: tenant, Revision: row.Revision, Plan: row.Plan, Suspended: row.Suspended, AuthOrganizationID: pgUUIDString(row.AuthOrganizationID)},
		row.TrialStartsAt, row.TrialEndsAt), row.PaymentMethodAt, row.TrialResolvedAt), nil
}

// withProductTrial adds a customer's stored trial (ADR-20261021195901).
func withProductTrial(c api.ProductCustomer, starts, ends pgtype.Timestamptz) api.ProductCustomer {
	if starts.Valid && ends.Valid {
		s, e := starts.Time.UTC(), ends.Time.UTC()
		c.TrialStartsAt, c.TrialEndsAt = &s, &e
	}
	return c
}

func (s *PgStore) PutProductCatalog(ctx context.Context, account, appID string, spec api.ProductCatalogSpec) (api.ProductCatalog, error) {
	return s.putProductCatalog(ctx, account, appID, spec, nil)
}

func (s *PgStore) PublishProductCatalog(ctx context.Context, account, appID string, spec api.ProductCatalogSpec, expected string) (api.ProductCatalog, error) {
	if err := validateProductExpectedHead(expected); err != nil {
		return api.ProductCatalog{}, err
	}
	return s.putProductCatalog(ctx, account, appID, spec, &expected)
}

func (s *PgStore) putProductCatalog(ctx context.Context, account, appID string, spec api.ProductCatalogSpec, expected *string) (api.ProductCatalog, error) {
	catalog, err := productcontrol.Compile(spec)
	if err != nil {
		return api.ProductCatalog{}, productCatalogError(err)
	}
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockCustomerOperationAccount(ctx, tx, a); err != nil {
		return api.ProductCatalog{}, mapErr(err)
	}
	if _, err := q.ProductControlOwnedApp(ctx, tx, sqlc.ProductControlOwnedAppParams{AccountID: a, ID: b}); err != nil {
		return api.ProductCatalog{}, mapErr(err)
	}
	previous, err := productCatalogTx(ctx, tx, account, appID, "")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return api.ProductCatalog{}, err
	}
	if expected != nil && previous.Revision != *expected && previous.Revision != catalog.Revision {
		return api.ProductCatalog{}, ErrProductCatalogStale
	}
	if err == nil && previous.Spec.Currency != spec.Currency {
		return api.ProductCatalog{}, ErrConflict
	}
	for _, feature := range spec.Features {
		if feature.Operation != nil {
			continue
		}
		id, _ := operationUUID(feature.DefinitionID)
		def, err := q.GetCustomerOperationDefinition(ctx, tx, sqlc.GetCustomerOperationDefinitionParams{AccountID: a, ID: id})
		if err != nil {
			return api.ProductCatalog{}, mapErr(err)
		}
		if def.AppID != appID {
			return api.ProductCatalog{}, ErrNotFound
		}
	}
	encoded, err := json.Marshal(catalog.Spec)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	if err := q.ProductControlPutRevision(ctx, tx, sqlc.ProductControlPutRevisionParams{AccountID: a, AppID: b, Revision: catalog.Revision, Spec: encoded}); err != nil {
		return api.ProductCatalog{}, err
	}
	if err := q.ProductControlSetHead(ctx, tx, sqlc.ProductControlSetHeadParams{AccountID: a, AppID: b, Revision: catalog.Revision}); err != nil {
		return api.ProductCatalog{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.ProductCatalog{}, err
	}
	return catalog, nil
}

func (s *PgStore) GetProductCatalog(ctx context.Context, account, appID, revision string) (api.ProductCatalog, error) {
	return productCatalogTx(ctx, s.pool, account, appID, revision)
}

func (s *PgStore) SetProductCustomer(ctx context.Context, account, appID string, customer api.ProductCustomer) (api.ProductCustomer, error) {
	if err := validateProductCustomerIdentity(customer); err != nil {
		return api.ProductCustomer{}, err
	}
	var org pgtype.UUID
	if customer.AuthOrganizationID != "" {
		var err error
		org, err = operationUUID(customer.AuthOrganizationID)
		if err != nil {
			return api.ProductCustomer{}, err
		}
	}
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	c, err := operationUUID(customer.TenantID)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockCustomerOperationAccount(ctx, tx, a); err != nil {
		return api.ProductCustomer{}, mapErr(err)
	}
	if _, err := q.ProductControlOwnedApp(ctx, tx, sqlc.ProductControlOwnedAppParams{AccountID: a, ID: b}); err != nil {
		return api.ProductCustomer{}, mapErr(err)
	}
	if _, err := q.LockCustomerOperationTenant(ctx, tx, sqlc.LockCustomerOperationTenantParams{AccountID: a, TenantID: c}); err != nil {
		return api.ProductCustomer{}, mapErr(err)
	}
	catalog, err := productCatalogTx(ctx, tx, account, appID, customer.Revision)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	if _, ok := catalog.Spec.Plans[customer.Plan]; !ok {
		return api.ProductCustomer{}, ErrNotFound
	}
	customer.Revision = catalog.Revision
	if err := q.ProductControlSetCustomer(ctx, tx, sqlc.ProductControlSetCustomerParams{AccountID: a, AppID: b, TenantID: c, Revision: customer.Revision, Plan: customer.Plan, Suspended: customer.Suspended, AuthOrganizationID: org}); err != nil {
		return api.ProductCustomer{}, mapErr(err)
	}
	// The stored assignment carries any trial it started or kept
	// (ADR-20261021195901).
	stored, err := productCustomerTx(ctx, tx, account, appID, customer.TenantID)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.ProductCustomer{}, err
	}
	return stored, nil
}

func (s *PgStore) GetProductCustomer(ctx context.Context, account, appID, tenant string) (api.ProductCustomer, error) {
	return productCustomerTx(ctx, s.pool, account, appID, tenant)
}

func (s *PgStore) ListProductCustomers(ctx context.Context, account, appID, cursor string) (api.ProductCustomersResponse, error) {
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductCustomersResponse{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return api.ProductCustomersResponse{}, err
	}
	rows, err := sqlc.New().ProductControlListCustomers(ctx, s.pool, sqlc.ProductControlListCustomersParams{AccountID: a, AppID: b, Cursor: cursor})
	if err != nil {
		return api.ProductCustomersResponse{}, err
	}
	out := api.ProductCustomersResponse{Customers: []api.ProductCustomer{}}
	for _, row := range rows {
		out.Customers = append(out.Customers, withProductConversion(withProductTrial(api.ProductCustomer{TenantID: pgUUIDString(row.TenantID), Revision: row.Revision, Plan: row.Plan, Suspended: row.Suspended,
			AuthOrganizationID: pgUUIDString(row.AuthOrganizationID)}, row.TrialStartsAt, row.TrialEndsAt), row.PaymentMethodAt, row.TrialResolvedAt))
	}
	if len(out.Customers) > api.ProductControlCustomerPageSize {
		out.Customers = out.Customers[:api.ProductControlCustomerPageSize]
		out.NextCursor = out.Customers[len(out.Customers)-1].TenantID
	}
	return out, nil
}

func (s *PgStore) GetProductUsage(ctx context.Context, account, appID, tenant, period string) (api.ProductUsage, error) {
	if err := productcontrol.ValidatePeriod(period); err != nil {
		return api.ProductUsage{}, ErrInvalidArgument
	}
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductUsage{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return api.ProductUsage{}, err
	}
	c, err := operationUUID(tenant)
	if err != nil {
		return api.ProductUsage{}, err
	}
	catalog, err := productCatalogTx(ctx, s.pool, account, appID, "")
	if err != nil {
		return api.ProductUsage{}, err
	}
	rows, err := sqlc.New().ProductControlUsage(ctx, s.pool, sqlc.ProductControlUsageParams{AccountID: a, AppID: b, TenantID: c, Period: period})
	if err != nil {
		return api.ProductUsage{}, err
	}
	out := api.ProductUsage{Period: period, Currency: catalog.Spec.Currency, Provisional: true, Lines: []api.ProductUsageLine{}}
	for _, row := range rows {
		out.Lines = append(out.Lines, api.ProductUsageLine{Feature: row.Feature, Revision: row.Revision, Plan: row.Plan, Starts: row.Starts, Succeeded: row.Succeeded, AmountMillicents: row.AmountMillicents})
	}
	// Starts during the trial use no included starts (ADR-20261021195901).
	customer, err := productCustomerTx(ctx, s.pool, account, appID, tenant)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return api.ProductUsage{}, err
	}
	if from, until, trial := productTrialInPeriod(customer, period); trial {
		counts, err := sqlc.New().ProductControlTrialStartsByFeature(ctx, s.pool, sqlc.ProductControlTrialStartsByFeatureParams{AccountID: a, AppID: b, TenantID: c,
			Period: period, TrialFrom: pgTime(from), TrialUntil: pgTime(until)})
		if err != nil {
			return api.ProductUsage{}, err
		}
		for _, row := range counts {
			if out.TrialStarts == nil {
				out.TrialStarts = map[string]int64{}
			}
			out.TrialStarts[row.Feature] = row.Starts
		}
	}
	return out, nil
}

// The caller already holds the Operations account and tenant admission locks.
func prepareProductOperationTx(ctx context.Context, tx pgx.Tx, op *Operation, def OperationDefinition, admission OperationAdmission, now time.Time) error {
	a, _ := operationUUID(op.AccountID)
	b, _ := operationUUID(op.AppID)
	c, _ := operationUUID(op.PlatformTenantID)
	q := sqlc.New()
	managed, err := q.ProductControlManagedApp(ctx, tx, sqlc.ProductControlManagedAppParams{AccountID: a, AppID: b})
	if err != nil {
		return err
	}
	if !managed && admission.ProductFeature == "" {
		return nil
	}
	customer, err := productCustomerTx(ctx, tx, op.AccountID, op.AppID, op.PlatformTenantID)
	if errors.Is(err, ErrNotFound) {
		return ErrProductAccess
	}
	if err != nil {
		return err
	}
	catalog, err := productCatalogTx(ctx, tx, op.AccountID, op.AppID, customer.Revision)
	if err != nil {
		return err
	}
	grant, err := productGrant(catalog, customer, admission.ProductFeature, def, admission.ProductControlEnabled)
	if err != nil {
		return err
	}
	if hold, err := productOverdueHoldPG(ctx, tx, op.AccountID, op.AppID, op.PlatformTenantID); err == nil {
		return productOverdueRefusal(hold)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if customer.AwaitsPaymentMethod(catalog.Spec.Plans[customer.Plan], now) {
		return ErrProductPaymentMethodRequired
	}
	feature := catalog.Spec.Features[admission.ProductFeature]
	if feature.Operation != nil {
		current, err := productDefinitionTx(ctx, tx, op.AccountID, op.AppID, feature)
		if err != nil {
			return err
		}
		if current.ID != op.DefinitionID {
			return ErrProductBindingUnavailable
		}
	}
	starts, err := q.ProductControlCountStarts(ctx, tx, sqlc.ProductControlCountStartsParams{AccountID: a, AppID: b, TenantID: c, Feature: admission.ProductFeature, Period: productcontrol.Period(now)})
	if err != nil {
		return err
	}
	var trialStarts int64
	if from, until, trial := productTrialInMonth(customer, now); trial {
		if trialStarts, err = q.ProductControlCountTrialStarts(ctx, tx, sqlc.ProductControlCountTrialStartsParams{AccountID: a, AppID: b, TenantID: c,
			Feature: admission.ProductFeature, Period: productcontrol.Period(now), TrialFrom: pgTime(from), TrialUntil: pgTime(until)}); err != nil {
			return err
		}
	}
	return prepareProductReceipt(op, admission, catalog, customer, grant, starts, trialStarts, now)
}

func insertProductOperationTx(ctx context.Context, tx pgx.Tx, op Operation) error {
	r := op.ProductControl
	if r == nil {
		return nil
	}
	a, _ := operationUUID(op.AccountID)
	b, _ := operationUUID(op.AppID)
	c, _ := operationUUID(op.PlatformTenantID)
	id, _ := operationUUID(op.ID)
	var evidence []byte
	if r.Admission != nil {
		var err error
		evidence, err = json.Marshal(r.Admission)
		if err != nil {
			return err
		}
	}
	return sqlc.New().ProductControlInsertReceipt(ctx, tx, sqlc.ProductControlInsertReceiptParams{OperationID: id, AccountID: a, AppID: b, TenantID: c, Revision: r.Revision, Feature: r.Feature, Plan: r.Plan, Currency: r.Currency, Period: r.Period, UnitPriceMillicents: r.UnitPriceMillicents, AdmittedAt: pgtype.Timestamptz{Time: r.AdmittedAt, Valid: true}, AdmissionEvidence: evidence})
}

func settleProductOperationTx(ctx context.Context, tx pgx.Tx, op Operation) error {
	if op.ProductControl == nil || !op.State.Terminal() {
		return nil
	}
	id, _ := operationUUID(op.ID)
	return sqlc.New().ProductControlSettleReceipt(ctx, tx, sqlc.ProductControlSettleReceiptParams{OperationID: id, ResolvedState: string(op.State), ResolvedAt: pgtype.Timestamptz{Time: op.UpdatedAt, Valid: true}})
}

func (s *PgStore) ProductCustomerByAuthOrganization(ctx context.Context, account, appID, org string) (api.ProductCustomer, error) {
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	c, err := operationUUID(org)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	row, err := sqlc.New().ProductControlCustomerByAuthOrganization(ctx, s.pool, sqlc.ProductControlCustomerByAuthOrganizationParams{AccountID: a, AppID: b, AuthOrganizationID: c})
	if err != nil {
		return api.ProductCustomer{}, mapErr(err)
	}
	return withProductConversion(withProductTrial(api.ProductCustomer{TenantID: pgUUIDString(row.TenantID), Revision: row.Revision, Plan: row.Plan, Suspended: row.Suspended, AuthOrganizationID: pgUUIDString(row.AuthOrganizationID)},
		row.TrialStartsAt, row.TrialEndsAt), row.PaymentMethodAt, row.TrialResolvedAt), nil
}
