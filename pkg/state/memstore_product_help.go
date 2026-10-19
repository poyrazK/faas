package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func (m *MemStore) helpViewLocked(s ProductHelpScope) (productHelpView, error) {
	var v productHelpView
	if err := validateProductPlanScope(s.ProductPlanScope); err != nil {
		return v, err
	}
	if _, err := m.onboardingOwnerLocked(s.AccountID, s.AppID); err != nil {
		return v, err
	}
	if _, err := m.onboardingAuthAppLocked(s.AccountID, api.ProductOnboardingRequest{AuthApp: s.AuthApp, AuthAppID: s.AuthAppID}); err != nil {
		return v, ErrProductAccess
	}
	d := m.productLocked()
	v.customer = d.customers[s.AppID+"/"+s.TenantID]
	v.head = d.revisions[s.AppID+"/"+d.heads[s.AppID]]
	v.current = d.revisions[s.AppID+"/"+v.customer.Revision]
	v.permissions = productcontrol.EffectiveAccess(v.head.Spec.Access, s.Role)
	return v, helpAuthorized(s, v, m.platformTenants[s.TenantID])
}

func (m *MemStore) helpCaptureLocked(scope ProductHelpScope, v productHelpView, req api.ProductHelpCreate, now time.Time) (api.ProductHelpContext, error) {
	c := helpContext(v, now)
	switch req.Kind {
	case "feature":
		feature, ok := v.current.Spec.Features[req.Feature]
		if !ok {
			return c, ErrNotFound
		}
		available, starts := false, int64(0)
		if v.permissions.RunWork {
			_, err := m.productDefinitionLocked(scope.AccountID, scope.AppID, feature)
			if err != nil && !errors.Is(err, ErrProductBindingUnavailable) && !errors.Is(err, ErrNotFound) {
				return c, err
			}
			available = err == nil
		}
		for _, row := range m.productLocked().receipts {
			if row.account == scope.AccountID && row.app == scope.AppID && row.tenant == scope.TenantID && row.receipt.Feature == req.Feature && row.receipt.Period == c.Period {
				starts++
			}
		}
		return helpFeatureContext(v, req, starts, available, now)
	case "run":
		row, ok := m.productLocked().receipts[req.OperationID]
		if !ok || row.account != scope.AccountID || row.app != scope.AppID || row.tenant != scope.TenantID {
			return c, ErrNotFound
		}
		c.Revision, c.Plan, c.Period, c.Feature, c.Reason = row.receipt.Revision, row.receipt.Plan, row.receipt.Period, row.receipt.Feature, "run_reported"
	}
	return c, nil
}

func (m *MemStore) CreateProductHelp(ctx context.Context, scope ProductHelpScope, req api.ProductHelpCreate) (api.ProductHelpRequest, error) {
	if err := helpRequestValid(req); err != nil {
		return api.ProductHelpRequest{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.helpViewLocked(scope)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	if !productcontrol.HelpVisible(req.Kind, v.permissions) {
		return api.ProductHelpRequest{}, ErrProductAccess
	}
	d := m.productLocked()
	if old, ok := d.help[req.ID]; ok {
		if !helpRowVisible(scope, old, v.permissions) || old.fingerprint != productOwnerHash(req) {
			return api.ProductHelpRequest{}, ErrConflict
		}
		return cloneProductBilling(old.value), ctx.Err()
	}
	total, active := 0, 0
	for _, row := range d.help {
		if row.value.AccountID == scope.AccountID && row.value.AppID == scope.AppID && row.value.TenantID == scope.TenantID {
			total++
			if row.value.Status != "resolved" {
				active++
			}
		}
	}
	if err := helpCapacity(total, active); err != nil {
		return api.ProductHelpRequest{}, err
	}
	now := time.Now().UTC()
	c, err := m.helpCaptureLocked(scope, v, req, now)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	if err = ctx.Err(); err != nil {
		return api.ProductHelpRequest{}, err
	}
	if d.help == nil {
		d.help = map[string]productHelpRow{}
	}
	row := helpNewRow(scope, req, c, now)
	d.help[req.ID] = row
	return cloneProductBilling(row.value), nil
}

func (m *MemStore) GetProductHelp(ctx context.Context, scope ProductHelpScope, id string) (api.ProductHelpRequest, error) {
	if !validProductUUID(id) {
		return api.ProductHelpRequest{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.helpViewLocked(scope)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	row, ok := m.productLocked().help[id]
	if !ok || !helpRowVisible(scope, row, v.permissions) {
		return api.ProductHelpRequest{}, ErrNotFound
	}
	return cloneProductBilling(row.value), ctx.Err()
}

func (m *MemStore) listHelpLocked(account, app, tenant, status string, cursor productOwnerCursor, visible func(productHelpRow) bool) api.ProductHelpPage {
	rows := []api.ProductHelpRequest{}
	for _, row := range m.productLocked().help {
		r := row.value
		if r.AccountID != account || r.AppID != app || tenant != "" && r.TenantID != tenant || !helpMatches(r.Status, status) || !visible(row) {
			continue
		}
		if t, ok := m.platformTenants[r.TenantID]; !ok || t.AccountID != account {
			continue
		}
		if cursor.ID != "" && (r.CreatedAt.After(cursor.At) || r.CreatedAt.Equal(cursor.At) && r.Request.ID >= cursor.ID) {
			continue
		}
		rows = append(rows, r)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
				return rows[i].Request.ID > rows[j].Request.ID
			}
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		})
		if len(rows) > api.ProductControlHelpPageSize+1 {
			rows = rows[:api.ProductControlHelpPageSize+1]
		}
	}
	return helpPage(rows, cursor)
}

func (m *MemStore) ListProductHelp(ctx context.Context, scope ProductHelpScope, status, raw string) (api.ProductHelpPage, error) {
	cursor, err := helpCursor(scope.AccountID, scope.AppID, scope.TenantID, status, scope.AuthAppID+"/"+scope.OrganizationID, raw)
	if err != nil {
		return api.ProductHelpPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.helpViewLocked(scope)
	if err != nil {
		return api.ProductHelpPage{}, err
	}
	page := m.listHelpLocked(scope.AccountID, scope.AppID, scope.TenantID, status, cursor, func(row productHelpRow) bool { return helpRowVisible(scope, row, v.permissions) })
	return cloneProductBilling(page), ctx.Err()
}

func (m *MemStore) helpOwnerRowLocked(account, app, id string) (productHelpRow, error) {
	if !validProductUUID(id) {
		return productHelpRow{}, ErrInvalidArgument
	}
	if _, err := m.onboardingOwnerLocked(account, app); err != nil {
		return productHelpRow{}, err
	}
	row, ok := m.productLocked().help[id]
	tenant, exists := m.platformTenants[row.value.TenantID]
	if !ok || row.value.AccountID != account || row.value.AppID != app || !exists || tenant.AccountID != account {
		return productHelpRow{}, ErrNotFound
	}
	return row, nil
}

func (m *MemStore) GetProductOwnerHelp(ctx context.Context, account, app, id string) (api.ProductHelpRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, err := m.helpOwnerRowLocked(account, app, id)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	return cloneProductBilling(row.value), ctx.Err()
}

func (m *MemStore) ListProductOwnerHelp(ctx context.Context, account, app, tenant, status, raw string) (api.ProductHelpPage, error) {
	cursor, err := helpCursor(account, app, tenant, status, "owner", raw)
	if err != nil {
		return api.ProductHelpPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err = m.onboardingOwnerLocked(account, app); err != nil {
		return api.ProductHelpPage{}, err
	}
	page := m.listHelpLocked(account, app, tenant, status, cursor, func(productHelpRow) bool { return true })
	return cloneProductBilling(page), ctx.Err()
}

func (m *MemStore) UpdateProductHelp(ctx context.Context, account, app, id string, update api.ProductHelpUpdate) (api.ProductHelpRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, err := m.helpOwnerRowLocked(account, app, id)
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	value, err := helpUpdated(row.value, update, time.Now())
	if err != nil {
		return api.ProductHelpRequest{}, err
	}
	if err = ctx.Err(); err != nil {
		return api.ProductHelpRequest{}, err
	}
	row.value = value
	m.productLocked().help[id] = row
	return cloneProductBilling(value), nil
}
