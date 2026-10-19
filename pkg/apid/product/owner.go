package product

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

type ownerContext struct {
	app      state.App
	account  state.Account
	products state.ProductControlStore
	owner    state.ProductOwnerStore
	billing  state.ProductBillingStore
	tenants  state.PlatformTenantStore
	data     dashboard.ProductControlData
}

// SurfaceGate hides internal routes and assets before ServeMux redirects and
// method fallback, including requests that have no registered handler.
func (s *Service) SurfaceGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productControlPath(r.URL.Path) {
			s.Gate(next.ServeHTTP)(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func productControlPath(path string) bool {
	if strings.HasPrefix(path, "/v1/product-control/") || path == "/v1/platform-tenant-self/product" || strings.HasPrefix(path, "/v1/platform-tenant-self/product/") {
		return true
	}
	p := strings.SplitN(path, "/", 6)
	return len(p) >= 5 && p[0] == "" && p[2] == "apps" && p[3] != "" &&
		((p[1] == "dashboard" && p[4] == "product-control") || (p[1] == "v1" && p[4] == "product"))
}

func (s *Service) ownerContext(w http.ResponseWriter, r *http.Request, acct state.Account) (*ownerContext, bool) {
	products, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, false
	}
	owner, ok := s.Host.Store().(state.ProductOwnerStore)
	billing, billingOK := s.Host.Store().(state.ProductBillingStore)
	tenants, tenantsOK := s.Host.Store().(state.PlatformTenantStore)
	if !ok || !billingOK || !tenantsOK {
		api.WriteProblem(w, api.ErrCapacity("Product Control owner storage is unavailable"))
		return nil, false
	}
	return &ownerContext{app: app, account: acct, products: products, owner: owner, billing: billing, tenants: tenants, data: dashboard.ProductControlData{AppSlug: app.Slug, BaseURL: "/dashboard/apps/" + url.PathEscape(app.Slug) + "/product-control"}}, true
}

func (s *Service) OwnerConsole(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	query, err := ownerQuery(r, "tenant", "period", "customers_cursor", "attention_cursor", "runs_cursor", "documents_cursor", "document")
	if err == nil {
		err = s.loadOwnerConsole(w, r, c, query)
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func ownerQuery(r *http.Request, allowed ...string) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, state.ErrInvalidArgument
	}
	for key, values := range q {
		if !ownerAllowed(key, allowed) || len(values) != 1 || len(values[0]) > api.ProductControlOwnerCursorMaxBytes {
			return nil, state.ErrInvalidArgument
		}
	}
	return q, nil
}

func ownerAllowed(key string, allowed []string) bool {
	for _, v := range allowed {
		if key == v {
			return true
		}
	}
	return false
}

func (s *Service) loadOwnerConsole(w http.ResponseWriter, r *http.Request, c *ownerContext, q url.Values) error {
	c.data.Main = true
	c.data.Period = q.Get("period")
	if c.data.Period == "" {
		c.data.Period = productcontrol.Period(time.Now())
	}
	if productcontrol.ValidatePeriod(c.data.Period) != nil {
		return state.ErrInvalidArgument
	}
	catalog, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if errors.Is(err, state.ErrNotFound) {
		c.data.Main = false
		c.data.Notice = "Start with guided setup to publish this product's feature and plan terms."
		return nil
	}
	if err != nil {
		return err
	}
	c.data.LatestRevision = catalog.Revision
	for name, plan := range catalog.Spec.Plans {
		c.data.Plans = append(c.data.Plans, dashboard.ProductControlPlan{Name: name, Label: plan.Label})
	}
	sort.Slice(c.data.Plans, func(i, j int) bool { return c.data.Plans[i].Name < c.data.Plans[j].Name })
	c.data.LatestFeatures, err = ownerFeatures(r, c, catalog, nil, nil)
	if err != nil {
		return err
	}
	if err = s.ownerOverview(r, c, q); err != nil {
		return err
	}
	if q.Get("tenant") != "" {
		if err = s.ownerCustomer(r, c, q); err != nil {
			return err
		}
	}
	c.data.ReviewToken, err = s.ownerToken(w, c, "review", c.account.ID+"/"+c.app.ID)
	return err
}

func (s *Service) ownerOverview(r *http.Request, c *ownerContext, q url.Values) error {
	customers, err := c.products.ListProductCustomers(r.Context(), c.account.ID, c.app.ID, q.Get("customers_cursor"))
	if err != nil {
		return err
	}
	for _, customer := range customers.Customers {
		tenant, err := c.tenants.GetPlatformTenant(r.Context(), c.account.ID, customer.TenantID)
		if err != nil {
			return err
		}
		c.data.Customers = append(c.data.Customers, dashboard.ProductControlCustomer{TenantID: customer.TenantID, Name: tenant.Name, Status: string(tenant.Status), Plan: customer.Plan, Revision: customer.Revision, Suspended: customer.Suspended, URL: ownerURL(c, customer.TenantID, c.data.Period, nil)})
	}
	c.data.NextCustomersURL = ownerPageURL(c, q, "customers_cursor", customers.NextCursor)
	attention, err := c.owner.ListProductOwnerAttention(r.Context(), c.account.ID, c.app.ID, q.Get("attention_cursor"))
	if err != nil {
		return err
	}
	for _, item := range attention.Items {
		c.data.Attention = append(c.data.Attention, dashboard.ProductControlAttention{TenantID: item.TenantID, Period: item.Period, Unresolved: item.Unresolved, NeedsReview: item.NeedsReview, URL: ownerURL(c, item.TenantID, item.Period, nil)})
	}
	c.data.NextAttentionURL = ownerPageURL(c, q, "attention_cursor", attention.NextCursor)
	return nil
}

func (s *Service) ownerCustomer(r *http.Request, c *ownerContext, q url.Values) error {
	tenantID := q.Get("tenant")
	if !ownerUUID(tenantID) {
		return state.ErrInvalidArgument
	}
	tenant, err := c.tenants.GetPlatformTenant(r.Context(), c.account.ID, tenantID)
	if err != nil {
		return err
	}
	c.data.TenantID, c.data.TenantName, c.data.TenantStatus = tenant.ID, tenant.Name, string(tenant.Status)
	customer, err := c.products.GetProductCustomer(r.Context(), c.account.ID, c.app.ID, tenant.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	c.data.Customer = &customer
	catalog, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, customer.Revision)
	if err != nil {
		return err
	}
	usage, err := c.products.GetProductUsage(r.Context(), c.account.ID, c.app.ID, tenant.ID, c.data.Period)
	if err != nil {
		return err
	}
	c.data.Features, err = ownerFeatures(r, c, catalog, &customer, &usage)
	if err != nil {
		return err
	}
	scope := state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, TenantID: tenant.ID, Period: c.data.Period}
	review, err := c.billing.ReviewProductBilling(r.Context(), scope)
	if err != nil {
		return err
	}
	c.data.Billing = &review
	c.data.BillingAmount = ownerMoney(review.Currency, review.AmountMillicents)
	if err = s.ownerRuns(r, c, scope, q); err != nil {
		return err
	}
	return s.ownerDocuments(r, c, scope, q)
}

func ownerFeatures(r *http.Request, c *ownerContext, catalog api.ProductCatalog, customer *api.ProductCustomer, usage *api.ProductUsage) ([]dashboard.ProductControlFeature, error) {
	items := []dashboard.ProductControlFeature{}
	for name, feature := range catalog.Spec.Features {
		item := dashboard.ProductControlFeature{Name: name, Label: feature.Label, Binding: "Available"}
		_, err := c.owner.InspectProductBinding(r.Context(), c.account.ID, c.app.ID, feature)
		if errors.Is(err, state.ErrProductBindingUnavailable) {
			item.Binding = "Unavailable: inspect the Operation binding"
		} else if err != nil {
			return nil, err
		}
		if customer != nil {
			grant, enabled := catalog.Spec.Plans[customer.Plan].Features[name]
			item.Enabled = enabled && !customer.Suspended
			item.Limit = grant.MonthlyStarts
			item.Price = ownerMoney(catalog.Spec.Currency, grant.UnitPriceMillicents)
			if usage != nil {
				for _, line := range usage.Lines {
					if line.Feature == name {
						item.Used += line.Starts
					}
				}
			}
			item.Remaining = max(int64(0), item.Limit-item.Used)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *Service) ownerRuns(r *http.Request, c *ownerContext, scope state.ProductBillingScope, q url.Values) error {
	page, err := c.owner.ListProductOwnerRuns(r.Context(), scope, q.Get("runs_cursor"))
	if err != nil {
		return err
	}
	for _, run := range page.Runs {
		e := run.Evidence
		status := string(run.State)
		if e.ResolvedState != "" {
			status = string(e.ResolvedState)
		}
		if status == "" {
			status = "Outcome unavailable; review required"
		}
		if e.WaivedAt != nil {
			status += " · charge waived"
		}
		c.data.Runs = append(c.data.Runs, dashboard.ProductControlRun{ID: e.OperationID, Feature: e.Receipt.Feature, AdmittedAt: e.Receipt.AdmittedAt.UTC().Format(time.RFC3339), State: status, Price: ownerMoney(e.Receipt.Currency, e.Receipt.UnitPriceMillicents), Credited: ownerMoney(e.Receipt.Currency, run.CreditedMillicents), URL: c.data.BaseURL + "/runs/" + url.PathEscape(e.OperationID), CanWaive: e.ResolvedState == "" && e.WaivedAt == nil, CanCredit: e.ResolvedState == api.OperationSucceeded && e.WaivedAt == nil && c.data.Billing.Finalized != nil && run.CreditedMillicents < e.Receipt.UnitPriceMillicents})
	}
	c.data.NextRunsURL = ownerPageURL(c, q, "runs_cursor", page.NextCursor)
	return nil
}

func (s *Service) ownerDocuments(r *http.Request, c *ownerContext, scope state.ProductBillingScope, q url.Values) error {
	page, err := ownerDocumentPage(r, c, scope, q)
	if err != nil {
		return err
	}
	if err := s.withInvoices(r.Context(), c.account.ID, &page, true); err != nil {
		return err
	}
	for _, doc := range page.Documents {
		item := dashboard.ProductControlDocument{ID: doc.ID, Kind: doc.Kind, Amount: ownerMoney(doc.Currency, doc.AmountMillicents), CreatedAt: doc.CreatedAt.UTC().Format(time.RFC3339), OperationID: doc.OperationID, StatementID: doc.StatementID, Reason: doc.Reason}
		if doc.Invoice != nil {
			item.Exported, item.Invoice, item.InvoiceURL, item.DashboardURL = true, ownerInvoice(*doc.Invoice), doc.Invoice.HostedInvoiceURL, doc.Invoice.DashboardURL
		}
		handoff, err := c.billing.GetProductBillingHandoff(r.Context(), scope, doc.ID)
		if err == nil {
			item.Reference = handoff.ExternalInvoiceID
		} else if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		c.data.Documents = append(c.data.Documents, item)
	}
	c.data.NextDocumentsURL = ownerPageURL(c, q, "documents_cursor", page.NextCursor)
	return nil
}

func ownerDocumentPage(r *http.Request, c *ownerContext, scope state.ProductBillingScope, q url.Values) (api.ProductBillingDocuments, error) {
	if q.Get("document") == "" {
		return c.billing.ListProductBillingDocuments(r.Context(), scope, q.Get("documents_cursor"))
	}
	if q.Get("documents_cursor") != "" {
		return api.ProductBillingDocuments{}, state.ErrInvalidArgument
	}
	doc, err := c.owner.GetProductOwnerDocument(r.Context(), scope, q.Get("document"))
	return api.ProductBillingDocuments{Documents: []api.ProductBillingDocument{doc}}, err
}

// ownerInvoice summarises a statement's export and payment status.
func ownerInvoice(e api.ProductDocumentInvoice) string {
	provider := map[string]string{"stripe": "Stripe", "paddle": "Paddle", "lago": "Lago"}[e.Provider]
	if e.ExportStatus != state.StatementInvoiceExportCompleted {
		return "Export to " + provider + " has not finished. Retry gregale product statements export; it resumes the same invoice."
	}
	status := e.PaymentStatus
	switch {
	case e.Overdue:
		status = "overdue"
	case status == "":
		status = "payment status not synced yet"
	}
	parts := []string{"Invoiced in " + provider + " as " + e.InvoiceID, status}
	if e.DueAt != nil {
		parts = append(parts, "due "+e.DueAt.UTC().Format("2006-01-02"))
	}
	if e.AmountRemainingMinor != nil {
		parts = append(parts, ownerMinor(e.Currency, *e.AmountRemainingMinor)+" remaining")
	}
	return strings.Join(parts, " · ")
}

// ownerMinor formats an amount in the currency's minor unit (cents).
func ownerMinor(currency string, n int64) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	return fmt.Sprintf("%s %s%d.%02d", currency, sign, n/100, n%100)
}

func ownerMoney(currency string, n int64) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	return fmt.Sprintf("%s %s%d.%05d", currency, sign, n/100000, n%100000)
}

func ownerURL(c *ownerContext, tenant, period string, extra url.Values) string {
	if extra == nil {
		extra = url.Values{}
	}
	if tenant != "" {
		extra.Set("tenant", tenant)
	}
	if period != "" {
		extra.Set("period", period)
	}
	return c.data.BaseURL + "?" + extra.Encode()
}

func ownerPageURL(c *ownerContext, q url.Values, key, cursor string) string {
	if cursor == "" {
		return ""
	}
	copy := url.Values{}
	for k, v := range q {
		copy[k] = append([]string(nil), v...)
	}
	copy.Set(key, cursor)
	return c.data.BaseURL + "?" + copy.Encode()
}

func ownerUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (s *Service) ownerError(w http.ResponseWriter, r *http.Request, c *ownerContext, err error) {
	status, message := http.StatusInternalServerError, "Product Control is temporarily unavailable."
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		status, message = 400, "Check the form fields and try again."
	case errors.Is(err, state.ErrNotFound):
		status, message = 404, "This customer, catalog, run, or billing document is unavailable."
	case errors.Is(err, state.ErrProductCustomerChangeStale), errors.Is(err, state.ErrProductBillingReviewChanged), errors.Is(err, state.ErrProductCatalogStale):
		status, message = 409, "The preview changed. Return to the customer and review again."
	case errors.Is(err, state.ErrProductBillingUnresolved):
		status, message = 409, "Reconcile unresolved runs or explicitly waive their charges before finalizing."
	case errors.Is(err, state.ErrProductBillingOpenPeriod):
		status, message = 409, "Only a closed UTC month can be finalized."
	case errors.Is(err, errDocumentExported):
		status, message = 409, "This statement was exported to your billing provider, which records its invoice as the handoff."
	case errors.Is(err, state.ErrConflict):
		status, message = 409, "This action conflicts with retained evidence. Review current assignments, credit balances, or invoice handoffs."
	}
	c.data.Error = message
	c.data.Confirmation = nil
	c.data.Main = false
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	s.OwnerRender(w, r, c.account, c.data)
}

func (s *Service) OwnerRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	after, err := explanationAfter(r.URL.RawQuery)
	if err == nil {
		var value api.ProductOperationExplanation
		value, err = explainOperation(r.Context(), c.products, acct.ID, c.app.ID, r.PathValue("operation"), after)
		c.data.Explanation = &value
		if value.NextGeneration > 0 {
			c.data.NextExecutionURL = r.URL.Path + "?after=" + strconv.Itoa(value.NextGeneration)
		}
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}
