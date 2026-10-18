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

func helpViewPG(ctx context.Context, tx pgx.Tx, scope ProductHelpScope) (productHelpView, error) {
	var v productHelpView
	if _, err := onboardingPGAuthApp(ctx, tx, scope.AccountID, api.ProductOnboardingRequest{AuthApp: scope.AuthApp, AuthAppID: scope.AuthAppID}); err != nil {
		if errors.Is(err, ErrProductOnboardingStale) {
			return v, ErrProductAccess
		}
		return v, err
	}
	var err error
	v.head, err = productCatalogTx(ctx, tx, scope.AccountID, scope.AppID, "")
	if err != nil {
		return v, err
	}
	v.customer, err = productCustomerTx(ctx, tx, scope.AccountID, scope.AppID, scope.TenantID)
	if err != nil {
		return v, err
	}
	tenant, err := sqlc.New().ProductOnboardingReadTenant(ctx, tx, sqlc.ProductOnboardingReadTenantParams{AccountID: mustPgUUID(scope.AccountID), ID: mustPgUUID(scope.TenantID)})
	if err != nil {
		return v, mapErr(err)
	}
	v.permissions = productcontrol.EffectiveAccess(v.head.Spec.Access, scope.Role)
	if err = helpAuthorized(scope, v, onboardingPGTenant(tenant)); err != nil {
		return v, err
	}
	v.current, err = productCatalogTx(ctx, tx, scope.AccountID, scope.AppID, v.customer.Revision)
	return v, err
}

func (s *PgStore) helpCustomerTx(ctx context.Context, scope ProductHelpScope) (pgx.Tx, productHelpView, error) {
	if err := validateProductPlanScope(scope.ProductPlanScope); err != nil {
		return nil, productHelpView{}, err
	}
	tx, err := s.productBillingTx(ctx, ProductBillingScope{AccountID: scope.AccountID, AppID: scope.AppID, TenantID: scope.TenantID, Period: productcontrol.Period(time.Now())})
	if err != nil {
		return nil, productHelpView{}, err
	}
	if _, err = onboardingPGOwner(ctx, tx, scope.AccountID, scope.AppID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, productHelpView{}, err
	}
	v, err := helpViewPG(ctx, tx, scope)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, v, err
	}
	return tx, v, nil
}

func helpPGRow(row sqlc.ProductControlHelpRequest) (productHelpRow, error) {
	r := productHelpRow{authApp: pgUUIDString(row.AuthAppID), org: pgUUIDString(row.AuthOrganizationID), fingerprint: row.Fingerprint, value: api.ProductHelpRequest{AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), TenantID: pgUUIDString(row.TenantID), Status: row.Status, Response: row.Response, Version: row.Version, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC()}}
	if err := json.Unmarshal(row.Request, &r.value.Request); err != nil {
		return r, err
	}
	if err := json.Unmarshal(row.Context, &r.value.Context); err != nil {
		return r, err
	}
	if r.value.Request.ID != pgUUIDString(row.ID) || r.value.Request.Kind != row.Kind || helpRequestValid(r.value.Request) != nil {
		return r, ErrConflict
	}
	return r, nil
}

func helpPGGet(ctx context.Context, tx pgx.Tx, account, app, id string) (productHelpRow, error) {
	if !validProductUUID(id) {
		return productHelpRow{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ProductHelpGet(ctx, tx, sqlc.ProductHelpGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id)})
	if err != nil {
		return productHelpRow{}, mapErr(err)
	}
	return helpPGRow(row)
}

func helpCapturePG(ctx context.Context, tx pgx.Tx, scope ProductHelpScope, v productHelpView, req api.ProductHelpCreate, now time.Time) (api.ProductHelpContext, error) {
	c := helpContext(v, now)
	q := sqlc.New()
	switch req.Kind {
	case "feature":
		feature, ok := v.current.Spec.Features[req.Feature]
		if !ok {
			return c, ErrNotFound
		}
		available := false
		if v.permissions.RunWork {
			_, err := productDefinitionTx(ctx, tx, scope.AccountID, scope.AppID, feature)
			if err != nil && !errors.Is(err, ErrProductBindingUnavailable) && !errors.Is(err, ErrNotFound) {
				return c, err
			}
			available = err == nil
		}
		rows, err := q.ProductPlanStarts(ctx, tx, sqlc.ProductPlanStartsParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), Period: c.Period, Features: []string{req.Feature}})
		if err != nil {
			return c, err
		}
		starts := int64(0)
		for _, row := range rows {
			starts += row.Starts
		}
		return helpFeatureContext(v, req, starts, available, now)
	case "run":
		row, err := q.ProductControlGetReceipt(ctx, tx, sqlc.ProductControlGetReceiptParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), OperationID: mustPgUUID(req.OperationID)})
		if err != nil {
			return c, mapErr(err)
		}
		if pgUUIDString(row.TenantID) != scope.TenantID {
			return c, ErrNotFound
		}
		c.Revision, c.Plan, c.Period, c.Feature, c.Reason = row.Revision, row.Plan, row.Period, row.Feature, "run_reported"
	}
	return c, nil
}

func (s *PgStore) CreateProductHelp(ctx context.Context, scope ProductHelpScope, req api.ProductHelpCreate) (api.ProductHelpRequest, error) {
	if err := helpRequestValid(req); err != nil {
		return api.ProductHelpRequest{}, err
	}
	tx, v, err := s.helpCustomerTx(ctx, scope)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !productcontrol.HelpVisible(req.Kind, v.permissions) {
		return api.ProductHelpRequest{}, ErrProductAccess
	}
	old, err := helpPGGet(ctx, tx, scope.AccountID, scope.AppID, req.ID)
	if err == nil {
		if !helpRowVisible(scope, old, v.permissions) || old.fingerprint != productOwnerHash(req) {
			return api.ProductHelpRequest{}, ErrConflict
		}
		return old.value, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return api.ProductHelpRequest{}, err
	}
	q := sqlc.New()
	counts, err := q.ProductHelpCount(ctx, tx, sqlc.ProductHelpCountParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID)})
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	if err = helpCapacity(int(counts.Total), int(counts.Active)); err != nil {
		return api.ProductHelpRequest{}, err
	}
	now := time.Now().UTC()
	c, err := helpCapturePG(ctx, tx, scope, v, req, now)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	row := helpNewRow(scope, req, c, now)
	request, err := json.Marshal(req)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	capture, err := json.Marshal(c)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	err = q.ProductHelpInsert(ctx, tx, sqlc.ProductHelpInsertParams{ID: mustPgUUID(req.ID), AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), AuthAppID: mustPgUUID(scope.AuthAppID), AuthOrganizationID: mustPgUUID(scope.OrganizationID), Fingerprint: row.fingerprint, Kind: req.Kind, Request: request, Context: capture, CreatedAt: pgtype.Timestamptz{Time: row.value.CreatedAt, Valid: true}})
	if err != nil {
		return api.ProductHelpRequest{}, mapErr(err)
	}
	return row.value, tx.Commit(ctx)
}

func (s *PgStore) GetProductHelp(ctx context.Context, scope ProductHelpScope, id string) (api.ProductHelpRequest, error) {
	tx, v, err := s.helpCustomerTx(ctx, scope)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := helpPGGet(ctx, tx, scope.AccountID, scope.AppID, id)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	if !helpRowVisible(scope, row, v.permissions) {
		return api.ProductHelpRequest{}, ErrNotFound
	}
	return row.value, tx.Commit(ctx)
}

func helpPGList(ctx context.Context, tx pgx.Tx, p sqlc.ProductHelpListParams, cursor productOwnerCursor) (api.ProductHelpPage, error) {
	p.AfterID, p.AfterAt, p.PageLimit = cursor.ID, pgtype.Timestamptz{Time: cursor.At, Valid: !cursor.At.IsZero()}, api.ProductControlHelpPageSize+1
	if cursor.ID != "" {
		p.AfterUuid = mustPgUUID(cursor.ID)
	}
	rows, err := sqlc.New().ProductHelpList(ctx, tx, p)
	if err != nil {
		return api.ProductHelpPage{}, err
	}
	values := []api.ProductHelpRequest{}
	for _, row := range rows {
		r, err := helpPGRow(row)
		if err != nil {
			return api.ProductHelpPage{}, err
		}
		values = append(values, r.value)
	}
	return helpPage(values, cursor), nil
}

func (s *PgStore) ListProductHelp(ctx context.Context, scope ProductHelpScope, status, raw string) (api.ProductHelpPage, error) {
	cursor, err := helpCursor(scope.AccountID, scope.AppID, scope.TenantID, status, scope.AuthAppID+"/"+scope.OrganizationID, raw)
	if err != nil {
		return api.ProductHelpPage{}, err
	}
	tx, v, err := s.helpCustomerTx(ctx, scope)
	if err != nil {
		return api.ProductHelpPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := helpPGList(ctx, tx, sqlc.ProductHelpListParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantFilter: mustPgUUID(scope.TenantID), StatusFilter: status, AuthAppFilter: mustPgUUID(scope.AuthAppID), OrgFilter: mustPgUUID(scope.OrganizationID), CanViewFeature: v.permissions.RunWork || v.permissions.ViewBilling, CanViewWork: v.permissions.ViewWork}, cursor)
	if err != nil {
		return page, err
	}
	return page, tx.Commit(ctx)
}

func (s *PgStore) GetProductOwnerHelp(ctx context.Context, account, app, id string) (api.ProductHelpRequest, error) {
	tx, err := s.onboardingTx(ctx, account, app)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := helpPGGet(ctx, tx, account, app, id)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	return row.value, tx.Commit(ctx)
}

func (s *PgStore) ListProductOwnerHelp(ctx context.Context, account, app, tenant, status, raw string) (api.ProductHelpPage, error) {
	cursor, err := helpCursor(account, app, tenant, status, "owner", raw)
	if err != nil {
		return api.ProductHelpPage{}, err
	}
	tx, err := s.onboardingTx(ctx, account, app)
	if err != nil {
		return api.ProductHelpPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p := sqlc.ProductHelpListParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), StatusFilter: status, OwnerMode: true}
	if tenant != "" {
		p.TenantFilter = mustPgUUID(tenant)
	}
	page, err := helpPGList(ctx, tx, p, cursor)
	if err != nil {
		return page, err
	}
	return page, tx.Commit(ctx)
}

func (s *PgStore) UpdateProductHelp(ctx context.Context, account, app, id string, update api.ProductHelpUpdate) (api.ProductHelpRequest, error) {
	tx, err := s.onboardingTx(ctx, account, app)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := helpPGGet(ctx, tx, account, app, id)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	value, err := helpUpdated(row.value, update, time.Now())
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	if value.Version != row.value.Version {
		n, err := sqlc.New().ProductHelpUpdate(ctx, tx, sqlc.ProductHelpUpdateParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id), Status: value.Status, Response: value.Response, Version: value.Version, UpdatedAt: pgtype.Timestamptz{Time: value.UpdatedAt, Valid: true}, Version_2: row.value.Version})
		if err != nil {
			return api.ProductHelpRequest{}, mapErr(err)
		}
		if n != 1 {
			return api.ProductHelpRequest{}, ErrConflict
		}
	}
	return value, tx.Commit(ctx)
}
