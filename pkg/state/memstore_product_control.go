package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/productemail"
)

type productMemory struct {
	email             *productemail.Memory
	heads             map[string]string
	revisions         map[string]api.ProductCatalog
	customers         map[string]api.ProductCustomer
	receipts          map[string]productMemoryReceipt
	documents         map[string]productMemoryDocument
	handoffs          map[string]api.ProductInvoiceHandoff
	changes           map[string]productMemoryChange
	onboardings       map[string]productMemoryOnboarding
	planChanges       map[string]productMemoryPlanChange
	help              map[string]productHelpRow
	notificationReads map[string]productNotificationRead
	// overduePolicies are keyed by app, overdueHolds by app/tenant.
	overduePolicies map[string]ProductOverduePolicy
	overdueHolds    map[string]ProductOverdueHold
	// planHistory is each customer's assignments by app/tenant, oldest
	// first (ADR-20261021154511).
	planHistory map[string][]productMemoryPlanEntry
}

type productMemoryPlanEntry struct {
	account, revision, plan string
	first                   bool
	at                      time.Time
}

// assignProductCustomerLocked stores an assignment and, when its revision or
// plan changed, appends it to the plan history, as the Postgres upsert does
// (ADR-20261021154511).
func (m *MemStore) assignProductCustomerLocked(account, app string, customer api.ProductCustomer) {
	d := m.productLocked()
	key := app + "/" + customer.TenantID
	prior, existed := d.customers[key]
	d.customers[key] = customer
	if existed && prior.Revision == customer.Revision && prior.Plan == customer.Plan {
		return
	}
	if d.planHistory == nil {
		d.planHistory = map[string][]productMemoryPlanEntry{}
	}
	d.planHistory[key] = append(d.planHistory[key], productMemoryPlanEntry{account: account, revision: customer.Revision, plan: customer.Plan, first: !existed, at: time.Now().UTC()})
}

// productMonthPlansLocked is ProductControlMonthPlans for the memory store.
func (m *MemStore) productMonthPlansLocked(scope ProductBillingScope) []productcontrol.PlanHeld {
	start, end := productMonthBounds(scope.Period)
	entries := m.productLocked().planHistory[scope.AppID+"/"+scope.TenantID]
	// The assignment in force at the month's start, and every later one.
	var inForce *time.Time
	for _, e := range entries {
		if e.account == scope.AccountID && !e.at.After(start) && (inForce == nil || e.at.After(*inForce)) {
			at := e.at
			inForce = &at
		}
	}
	held := []productcontrol.PlanHeld{}
	index := map[string]int{}
	for _, e := range entries {
		if e.account != scope.AccountID || !e.at.Before(end) || inForce != nil && e.at.Before(*inForce) {
			continue
		}
		key := e.revision + "/" + e.plan
		if at, ok := index[key]; ok {
			held[at].First = held[at].First || e.first
			continue
		}
		index[key] = len(held)
		held = append(held, productcontrol.PlanHeld{Revision: e.revision, Plan: e.plan, Since: e.at, First: e.first})
	}
	return held
}

type productMemoryReceipt struct {
	account, app, tenant string
	receipt              api.ProductOperationReceipt
	succeededAt          *time.Time
	resolvedState        api.OperationState
	resolvedAt           *time.Time
	waivedAt             *time.Time
	waiverReason         string
}

func (m *MemStore) checkProductCustomerIdentityLocked(def OperationDefinition, admission OperationAdmission) error {
	if admission.ProductAuthOrganizationID == "" && admission.ProductAuthApp == "" {
		return nil
	}
	d := m.productLocked()
	customer := d.customers[def.AppID+"/"+admission.PlatformTenantID]
	head := d.revisions[def.AppID+"/"+d.heads[def.AppID]]
	if customer.AuthOrganizationID == "" || customer.AuthOrganizationID != admission.ProductAuthOrganizationID || head.Spec.AuthApp == "" || head.Spec.AuthApp != admission.ProductAuthApp {
		return ErrProductAccess
	}
	return nil
}

func (m *MemStore) productLocked() *productMemory {
	if m.productData == nil {
		m.productData = &productMemory{heads: map[string]string{}, revisions: map[string]api.ProductCatalog{}, customers: map[string]api.ProductCustomer{}, receipts: map[string]productMemoryReceipt{}, documents: map[string]productMemoryDocument{}, handoffs: map[string]api.ProductInvoiceHandoff{}}
	}
	return m.productData
}

func (m *MemStore) PutProductCatalog(ctx context.Context, account, appID string, spec api.ProductCatalogSpec) (api.ProductCatalog, error) {
	return m.putProductCatalog(ctx, account, appID, spec, nil)
}

func (m *MemStore) PublishProductCatalog(ctx context.Context, account, appID string, spec api.ProductCatalogSpec, expected string) (api.ProductCatalog, error) {
	if err := validateProductExpectedHead(expected); err != nil {
		return api.ProductCatalog{}, err
	}
	return m.putProductCatalog(ctx, account, appID, spec, &expected)
}

func (m *MemStore) putProductCatalog(_ context.Context, account, appID string, spec api.ProductCatalogSpec, expected *string) (api.ProductCatalog, error) {
	catalog, err := productcontrol.Compile(spec)
	if err != nil {
		return api.ProductCatalog{}, productCatalogError(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, exists := m.apps[appID]
	if !exists || app.AccountID != account || app.Status == AppDeleted {
		return api.ProductCatalog{}, ErrNotFound
	}
	d := m.productLocked()
	if expected != nil && d.heads[appID] != *expected && d.heads[appID] != catalog.Revision {
		return api.ProductCatalog{}, ErrProductCatalogStale
	}
	if previous, ok := d.revisions[appID+"/"+d.heads[appID]]; ok && previous.Spec.Currency != spec.Currency {
		return api.ProductCatalog{}, ErrConflict
	}
	for _, feature := range spec.Features {
		if feature.Operation != nil {
			continue
		}
		def, ok := m.operationMemoryLocked().definitions[feature.DefinitionID]
		if !ok || def.AccountID != account || def.AppID != appID {
			return api.ProductCatalog{}, ErrNotFound
		}
	}
	d.revisions[appID+"/"+catalog.Revision] = catalog
	d.heads[appID] = catalog.Revision
	return productcontrol.Compile(catalog.Spec)
}

func (m *MemStore) GetProductCatalog(_ context.Context, account, appID, revision string) (api.ProductCatalog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[appID]; !ok || app.AccountID != account || app.Status == AppDeleted {
		return api.ProductCatalog{}, ErrNotFound
	}
	d := m.productLocked()
	if revision == "" {
		revision = d.heads[appID]
	}
	catalog, ok := d.revisions[appID+"/"+revision]
	if !ok {
		return api.ProductCatalog{}, ErrNotFound
	}
	return productcontrol.Compile(catalog.Spec)
}

func (m *MemStore) SetProductCustomer(_ context.Context, account, appID string, customer api.ProductCustomer) (api.ProductCustomer, error) {
	if err := validateProductCustomerIdentity(customer); err != nil {
		return api.ProductCustomer{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.productLocked()
	app, exists := m.apps[appID]
	tenant, tenantExists := m.platformTenants[customer.TenantID]
	if !exists || app.AccountID != account || app.Status == AppDeleted || !tenantExists || tenant.AccountID != account {
		return api.ProductCustomer{}, ErrNotFound
	}
	if customer.Revision == "" {
		customer.Revision = d.heads[appID]
	}
	catalog, exists := d.revisions[appID+"/"+customer.Revision]
	if _, ok := catalog.Spec.Plans[customer.Plan]; !exists || !ok {
		return api.ProductCustomer{}, ErrNotFound
	}
	for key, assigned := range d.customers {
		if customer.AuthOrganizationID != "" && key == appID+"/"+assigned.TenantID && assigned.TenantID != customer.TenantID && assigned.AuthOrganizationID == customer.AuthOrganizationID {
			return api.ProductCustomer{}, ErrConflict
		}
	}
	m.assignProductCustomerLocked(account, appID, customer)
	return customer, nil
}

func (m *MemStore) GetProductCustomer(_ context.Context, account, appID, tenantID string) (api.ProductCustomer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != account || app.Status == AppDeleted {
		return api.ProductCustomer{}, ErrNotFound
	}
	customer, ok := m.productLocked().customers[appID+"/"+tenantID]
	if !ok {
		return api.ProductCustomer{}, ErrNotFound
	}
	return customer, nil
}

func (m *MemStore) ListProductCustomers(_ context.Context, account, appID, cursor string) (api.ProductCustomersResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[appID]; !ok || app.AccountID != account || app.Status == AppDeleted {
		return api.ProductCustomersResponse{}, ErrNotFound
	}
	response := api.ProductCustomersResponse{Customers: []api.ProductCustomer{}}
	for key, customer := range m.productLocked().customers {
		if key == appID+"/"+customer.TenantID && customer.TenantID > cursor {
			response.Customers = append(response.Customers, customer)
		}
	}
	sort.Slice(response.Customers, func(i, j int) bool { return response.Customers[i].TenantID < response.Customers[j].TenantID })
	if len(response.Customers) > api.ProductControlCustomerPageSize {
		response.Customers = response.Customers[:api.ProductControlCustomerPageSize]
		response.NextCursor = response.Customers[len(response.Customers)-1].TenantID
	}
	return response, nil
}

func (m *MemStore) GetProductUsage(_ context.Context, account, appID, tenantID, period string) (api.ProductUsage, error) {
	if _, err := operationUUID(tenantID); err != nil {
		return api.ProductUsage{}, err
	}
	if err := productcontrol.ValidatePeriod(period); err != nil {
		return api.ProductUsage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[appID]; !ok || app.AccountID != account || app.Status == AppDeleted {
		return api.ProductUsage{}, ErrNotFound
	}
	d := m.productLocked()
	catalog, ok := d.revisions[appID+"/"+d.heads[appID]]
	if !ok {
		return api.ProductUsage{}, ErrNotFound
	}
	lines := map[string]api.ProductUsageLine{}
	for _, row := range d.receipts {
		r := row.receipt
		if row.account != account || row.app != appID || row.tenant != tenantID || r.Period != period {
			continue
		}
		key := r.Feature + "/" + r.Revision + "/" + r.Plan
		line := lines[key]
		line.Feature, line.Revision, line.Plan = r.Feature, r.Revision, r.Plan
		line.Starts++
		if row.succeededAt != nil {
			line.Succeeded++
			if row.waivedAt == nil {
				line.AmountMillicents += r.UnitPriceMillicents
			}
		}
		lines[key] = line
	}
	return api.ProductUsage{Period: period, Currency: catalog.Spec.Currency, Lines: productUsageLines(lines), Provisional: true}, nil
}

// Called under the same lock as Operations admission, after duplicate lookup.
func (m *MemStore) prepareProductOperationLocked(op *Operation, def OperationDefinition, admission OperationAdmission, now time.Time) error {
	d := m.productLocked()
	managed := d.heads[op.AppID] != ""
	if !managed && admission.ProductFeature == "" {
		return nil
	}
	customer, ok := d.customers[op.AppID+"/"+op.PlatformTenantID]
	if !ok {
		return ErrProductAccess
	}
	catalog := d.revisions[op.AppID+"/"+customer.Revision]
	grant, err := productGrant(catalog, customer, admission.ProductFeature, def, admission.ProductControlEnabled)
	if err != nil {
		return err
	}
	if hold, held := m.productOverdueHoldLocked(op.AccountID, op.AppID, op.PlatformTenantID); held {
		return productOverdueRefusal(hold)
	}
	feature := catalog.Spec.Features[admission.ProductFeature]
	if feature.Operation != nil {
		current, err := m.productDefinitionLocked(op.AccountID, op.AppID, feature)
		if err != nil || current.ID != op.DefinitionID {
			return ErrProductBindingUnavailable
		}
	}
	var starts int64
	for _, r := range d.receipts {
		if r.app == op.AppID && r.tenant == op.PlatformTenantID && r.receipt.Feature == admission.ProductFeature && r.receipt.Period == productcontrol.Period(now) {
			starts++
		}
	}
	return prepareProductReceipt(op, admission, catalog, customer, grant, starts, now)
}

func (m *MemStore) insertProductOperationLocked(op Operation) {
	if op.ProductControl == nil {
		return
	}
	m.productLocked().receipts[op.ID] = productMemoryReceipt{account: op.AccountID, app: op.AppID, tenant: op.PlatformTenantID, receipt: *op.ProductControl}
}

func (m *MemStore) settleProductOperationLocked(op Operation) {
	if op.ProductControl == nil || !op.State.Terminal() {
		return
	}
	d := m.productLocked()
	r, ok := d.receipts[op.ID]
	if ok && r.resolvedState == "" {
		completed := op.UpdatedAt
		r.resolvedAt, r.resolvedState = &completed, op.State
		if op.State == api.OperationSucceeded {
			r.succeededAt = &completed
		}
		d.receipts[op.ID] = r
	}
}

func (m *MemStore) ProductCustomerByAuthOrganization(_ context.Context, account, appID, org string) (api.ProductCustomer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[appID]; !ok || app.AccountID != account || app.Status == AppDeleted || org == "" {
		return api.ProductCustomer{}, ErrNotFound
	}
	for key, customer := range m.productLocked().customers {
		if key == appID+"/"+customer.TenantID && customer.AuthOrganizationID == org {
			return customer, nil
		}
	}
	return api.ProductCustomer{}, ErrNotFound
}
