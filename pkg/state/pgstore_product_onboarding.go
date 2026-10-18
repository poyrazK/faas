package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func onboardingPGOwner(ctx context.Context, db sqlc.DBTX, account, app string) (api.Plan, error) {
	a, err := operationUUID(account)
	if err != nil {
		return "", err
	}
	b, err := operationUUID(app)
	if err != nil {
		return "", err
	}
	plan, err := sqlc.New().ProductOnboardingOwner(ctx, db, sqlc.ProductOnboardingOwnerParams{ID: a, ID_2: b})
	if err != nil {
		return "", mapErr(err)
	}
	return api.Plan(plan), nil
}

func (s *PgStore) onboardingTx(ctx context.Context, account, app string) (pgx.Tx, error) {
	if _, err := operationUUID(account); err != nil {
		return nil, err
	}
	if _, err := operationUUID(app); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = sqlc.New().LockCustomerOperationAccount(ctx, tx, mustPgUUID(account)); err == nil {
		_, err = onboardingPGOwner(ctx, tx, account, app)
	}
	if err == nil {
		_, err = sqlc.New().ProductControlOwnedApp(ctx, tx, sqlc.ProductControlOwnedAppParams{AccountID: mustPgUUID(account), ID: mustPgUUID(app)})
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, mapErr(err)
	}
	return tx, nil
}

func onboardingPGAuthApp(ctx context.Context, db sqlc.DBTX, account string, req api.ProductOnboardingRequest) (App, error) {
	id, err := operationUUID(req.AuthAppID)
	if err != nil {
		return App{}, err
	}
	value, err := sqlc.New().ProductOnboardingAuthApp(ctx, db, sqlc.ProductOnboardingAuthAppParams{AccountID: mustPgUUID(account), ID: id, Slug: req.AuthApp})
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrProductOnboardingStale
	}
	return App{ID: pgUUIDString(value), AccountID: account, Slug: req.AuthApp}, err
}

func onboardingPGTenant(row sqlc.PlatformTenant) PlatformTenant {
	return PlatformTenant{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), ExternalRef: row.ExternalRef, Name: row.Name, Status: row.Status, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}

func previewProductOnboardingPG(ctx context.Context, tx pgx.Tx, account, app string, req api.ProductOnboardingRequest) (api.ProductOnboardingPreview, error) {
	plan, err := onboardingPGOwner(ctx, tx, account, app)
	if err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	if plan.ConsumerKeysPerAccount() == 0 {
		return api.ProductOnboardingPreview{}, ErrProductAccess
	}
	if _, err = onboardingPGAuthApp(ctx, tx, account, req); err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	head, err := productCatalogTx(ctx, tx, account, app, "")
	if err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	catalog, err := productCatalogTx(ctx, tx, account, app, req.Revision)
	if err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	p := sqlc.ProductOnboardingTenantParams{AccountID: mustPgUUID(account), ExternalRef: req.ExternalRef}
	if req.TenantID != "" {
		p.TenantID = mustPgUUID(req.TenantID)
	}
	row, err := sqlc.New().ProductOnboardingTenant(ctx, tx, p)
	var tenant *PlatformTenant
	if err == nil {
		t := onboardingPGTenant(row)
		tenant = &t
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.ProductOnboardingPreview{}, err
	} else if req.TenantID != "" {
		return api.ProductOnboardingPreview{}, ErrNotFound
	}
	var before *api.ProductCustomer
	if tenant != nil {
		c, e := productCustomerTx(ctx, tx, account, app, tenant.ID)
		if e == nil {
			before = &c
		} else if !errors.Is(e, ErrNotFound) {
			return api.ProductOnboardingPreview{}, e
		}
	}
	if req.Revision == "" && before != nil {
		catalog, err = productCatalogTx(ctx, tx, account, app, before.Revision)
		if err != nil {
			return api.ProductOnboardingPreview{}, err
		}
	}
	linked, err := sqlc.New().ProductControlCustomerByAuthOrganization(ctx, tx, sqlc.ProductControlCustomerByAuthOrganizationParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), AuthOrganizationID: mustPgUUID(req.OrganizationID)})
	if err == nil && (tenant == nil || pgUUIDString(linked.TenantID) != tenant.ID) {
		return api.ProductOnboardingPreview{}, ErrConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.ProductOnboardingPreview{}, err
	}
	count, err := sqlc.New().ProductOnboardingTenantCount(ctx, tx, mustPgUUID(account))
	if err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	return productOnboardingPreview(account, app, req, head, catalog, tenant, before, int(count), plan.ConsumerKeysPerAccount(), time.Now())
}

func (s *PgStore) PreviewProductOnboarding(ctx context.Context, account, app string, req api.ProductOnboardingRequest) (api.ProductOnboardingPreview, error) {
	if err := validateProductOnboarding(req); err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	tx, err := s.onboardingTx(ctx, account, app)
	if err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := previewProductOnboardingPG(ctx, tx, account, app, req)
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

func (s *PgStore) ApplyProductOnboarding(ctx context.Context, account, app string, c api.ProductOnboardingConfirmation) (api.ProductOnboardingReceipt, error) {
	if err := validateProductOnboardingConfirmation(c); err != nil {
		return api.ProductOnboardingReceipt{}, err
	}
	tx, err := s.onboardingTx(ctx, account, app)
	if err != nil {
		return api.ProductOnboardingReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	fingerprint := productOnboardingFingerprint(account, app, c)
	old, err := q.ProductOnboardingGet(ctx, tx, sqlc.ProductOnboardingGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(c.Request.ID)})
	if err == nil {
		if old.Fingerprint != fingerprint {
			return api.ProductOnboardingReceipt{}, ErrConflict
		}
		var r api.ProductOnboardingReceipt
		if err = json.Unmarshal(old.Receipt, &r); err != nil {
			return r, err
		}
		return r, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.ProductOnboardingReceipt{}, err
	}
	p, err := previewProductOnboardingPG(ctx, tx, account, app, c.Request)
	if err != nil {
		return api.ProductOnboardingReceipt{}, err
	}
	if p.Hash != c.ExpectedHash {
		return api.ProductOnboardingReceipt{}, ErrProductOnboardingStale
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenant := PlatformTenant{ID: p.Tenant.ID, AccountID: account, ExternalRef: p.Tenant.ExternalRef, Name: p.Tenant.Name, Status: p.Tenant.Status}
	if p.CreatesTenant {
		tenant.ID = uuid.NewString()
		err = q.ProductOnboardingCreateTenant(ctx, tx, sqlc.ProductOnboardingCreateTenantParams{ID: mustPgUUID(tenant.ID), AccountID: mustPgUUID(account), ExternalRef: tenant.ExternalRef, Name: tenant.Name, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			return api.ProductOnboardingReceipt{}, mapErr(err)
		}
	}
	r := newProductOnboardingReceipt(p, tenant, now)
	err = q.ProductControlSetCustomer(ctx, tx, sqlc.ProductControlSetCustomerParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), TenantID: mustPgUUID(tenant.ID), Revision: r.Customer.Revision, Plan: r.Customer.Plan, AuthOrganizationID: mustPgUUID(r.Customer.AuthOrganizationID)})
	if err != nil {
		return api.ProductOnboardingReceipt{}, mapErr(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	err = q.ProductOnboardingInsert(ctx, tx, sqlc.ProductOnboardingInsertParams{ID: mustPgUUID(r.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), TenantID: mustPgUUID(tenant.ID), AuthAppID: mustPgUUID(r.Request.AuthAppID), AuthOrganizationID: mustPgUUID(r.Request.OrganizationID), Fingerprint: fingerprint, Receipt: raw, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}})
	if err != nil {
		return api.ProductOnboardingReceipt{}, mapErr(err)
	}
	return r, tx.Commit(ctx)
}

func readProductOnboardingPG(ctx context.Context, db sqlc.DBTX, account, app string, row sqlc.ProductControlOnboarding) (api.ProductOnboardingStatus, error) {
	var receipt api.ProductOnboardingReceipt
	if err := json.Unmarshal(row.Receipt, &receipt); err != nil {
		return api.ProductOnboardingStatus{}, err
	}
	t, err := sqlc.New().ProductOnboardingReadTenant(ctx, db, sqlc.ProductOnboardingReadTenantParams{AccountID: mustPgUUID(account), ID: row.TenantID})
	if err != nil {
		return api.ProductOnboardingStatus{}, mapErr(err)
	}
	var current *api.ProductCustomer
	c, err := productCustomerTx(ctx, db, account, app, receipt.Tenant.ID)
	if err == nil {
		current = &c
	} else if !errors.Is(err, ErrNotFound) {
		return api.ProductOnboardingStatus{}, err
	}
	head, err := productCatalogTx(ctx, db, account, app, "")
	if err != nil {
		return api.ProductOnboardingStatus{}, err
	}
	authApp, err := onboardingPGAuthApp(ctx, db, account, receipt.Request)
	if err != nil && !errors.Is(err, ErrProductOnboardingStale) {
		return api.ProductOnboardingStatus{}, err
	}
	var access *time.Time
	if row.AccessVerifiedAt.Valid {
		v := row.AccessVerifiedAt.Time.UTC()
		access = &v
	}
	first, err := sqlc.New().ProductOnboardingFirstSuccess(ctx, db, sqlc.ProductOnboardingFirstSuccessParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), TenantID: row.TenantID, Since: row.CreatedAt})
	var success *api.ProductOnboardingSuccess
	if err == nil {
		success = &api.ProductOnboardingSuccess{OperationID: pgUUIDString(first.OperationID), Feature: first.Feature, VerifiedAt: first.ResolvedAt.Time.UTC()}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.ProductOnboardingStatus{}, err
	}
	return productOnboardingStatus(receipt, current, onboardingPGTenant(t), head, authApp, access, success), nil
}

func (s *PgStore) productOnboardingRead(ctx context.Context, account, app, id string, byTenant bool) (api.ProductOnboardingStatus, error) {
	if !validProductUUID(id) {
		return api.ProductOnboardingStatus{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ProductOnboardingStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = onboardingPGOwner(ctx, tx, account, app); err != nil {
		return api.ProductOnboardingStatus{}, err
	}
	var row sqlc.ProductControlOnboarding
	if byTenant {
		row, err = sqlc.New().ProductOnboardingLatest(ctx, tx, sqlc.ProductOnboardingLatestParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), TenantID: mustPgUUID(id)})
	} else {
		row, err = sqlc.New().ProductOnboardingGet(ctx, tx, sqlc.ProductOnboardingGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id)})
	}
	if err != nil {
		return api.ProductOnboardingStatus{}, mapErr(err)
	}
	status, err := readProductOnboardingPG(ctx, tx, account, app, row)
	if err != nil {
		return status, err
	}
	return status, tx.Commit(ctx)
}

func (s *PgStore) GetProductOnboarding(ctx context.Context, account, app, id string) (api.ProductOnboardingStatus, error) {
	return s.productOnboardingRead(ctx, account, app, id, false)
}

func (s *PgStore) ProductOnboardingByTenant(ctx context.Context, account, app, tenant string) (api.ProductOnboardingStatus, error) {
	return s.productOnboardingRead(ctx, account, app, tenant, true)
}

func (s *PgStore) ObserveProductCustomerAccess(ctx context.Context, account, app, tenant, authApp, org string) error {
	if !validProductUUID(tenant) || !validProductUUID(org) {
		return ErrInvalidArgument
	}
	tx, err := s.onboardingTx(ctx, account, app)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	t, err := sqlc.New().ProductOnboardingTenant(ctx, tx, sqlc.ProductOnboardingTenantParams{AccountID: mustPgUUID(account), TenantID: mustPgUUID(tenant)})
	if err != nil {
		return mapErr(err)
	}
	c, err := productCustomerTx(ctx, tx, account, app, tenant)
	if err != nil {
		return err
	}
	head, err := productCatalogTx(ctx, tx, account, app, "")
	if err != nil {
		return err
	}
	if c.Suspended || c.AuthOrganizationID != org || head.Spec.AuthApp != authApp || t.Status != PlatformTenantActive {
		return ErrProductAccess
	}
	err = sqlc.New().ProductOnboardingAccess(ctx, tx, sqlc.ProductOnboardingAccessParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), TenantID: mustPgUUID(tenant), OrganizationID: mustPgUUID(org), AuthApp: authApp, ObservedAt: pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Microsecond), Valid: true}})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
