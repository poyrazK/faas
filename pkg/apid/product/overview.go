package product

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) CustomersOverview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	products, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "cursor")
	if err != nil {
		respond(w, nil, err)
		return
	}
	page, err := s.customerOverview(r.Context(), acct, app, products, q.Get("cursor"))
	respond(w, page, err)
}

func (s *Service) OwnerCustomersOverview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "cursor")
	var page api.ProductCustomerOverviewPage
	if err == nil {
		page, err = s.customerOverview(r.Context(), acct, c.app, c.products, q.Get("cursor"))
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	c.data.Overview = customerOverviewView(c, page)
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) customerOverview(parent context.Context, acct state.Account, app state.App, products state.ProductControlStore, cursor string) (api.ProductCustomerOverviewPage, error) {
	page := api.ProductCustomerOverviewPage{AppID: app.ID, ObservedAt: time.Now().UTC(), ObservationScope: "responding_api_node", QualificationState: "unverified", Customers: []api.ProductCustomerOverview{}}
	page.Period = productcontrol.Period(page.ObservedAt)
	if cursor != "" && !ownerUUID(cursor) {
		return page, state.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(parent, api.ProductControlOverviewMaxDuration)
	defer cancel()
	customers, err := products.ListProductCustomers(ctx, acct.ID, app.ID, cursor)
	if err != nil {
		return page, err
	}
	page.NextCursor = customers.NextCursor
	if len(customers.Customers) > api.ProductControlOverviewPageSize {
		customers.Customers = customers.Customers[:api.ProductControlOverviewPageSize]
		page.NextCursor = customers.Customers[len(customers.Customers)-1].TenantID
	}
	for _, customer := range customers.Customers {
		row, err := s.customerOverviewRow(ctx, acct, app, products, customer, page.Period)
		if err != nil {
			return page, err
		}
		added, err := appendOverviewCustomer(&page, row)
		if err != nil {
			return page, err
		}
		if !added {
			break
		}
	}
	if ctx.Err() != nil {
		return page, ctx.Err()
	}
	if productcontrol.Period(time.Now()) != page.Period {
		return page, state.ErrConflict
	}
	return page, nil
}

func appendOverviewCustomer(page *api.ProductCustomerOverviewPage, row api.ProductCustomerOverview) (bool, error) {
	page.Customers = append(page.Customers, row)
	// Include a continuation cursor while measuring, even on a final source page.
	// If the following row does not fit, that cursor must fit the previous page.
	next := page.NextCursor
	page.NextCursor = row.Customer.TenantID
	raw, err := json.Marshal(page)
	page.NextCursor = next
	if err != nil {
		return false, err
	}
	if len(raw)+1 <= api.ProductControlOverviewMaxResponseBytes {
		return true, nil
	}
	page.Customers = page.Customers[:len(page.Customers)-1]
	if len(page.Customers) == 0 {
		return false, errors.New("customer diagnostics exceed the overview response bound")
	}
	page.NextCursor = page.Customers[len(page.Customers)-1].Customer.TenantID
	return false, nil
}

func (s *Service) customerOverviewRow(ctx context.Context, acct state.Account, app state.App, products state.ProductControlStore, customer api.ProductCustomer, period string) (api.ProductCustomerOverview, error) {
	row := api.ProductCustomerOverview{Customer: customer, NextActions: []api.ProductCustomerNextAction{}}
	tenants, ok := s.Host.Store().(state.PlatformTenantStore)
	onboarding, onboardingOK := s.Host.Store().(state.ProductOnboardingStore)
	work, workOK := s.Host.Store().(state.ProductCustomerOverviewStore)
	if !ok || !onboardingOK || !workOK {
		return row, state.ErrNotFound
	}
	tenant, err := tenants.GetPlatformTenant(ctx, acct.ID, customer.TenantID)
	if err != nil {
		return row, err
	}
	row.Name, row.TenantStatus = tenant.Name, string(tenant.Status)
	row.Readiness, err = s.doctorReport(ctx, acct, app, products, customer.TenantID)
	if err != nil {
		return row, err
	}
	status, err := onboarding.ProductOnboardingByTenant(ctx, acct.ID, app.ID, customer.TenantID)
	if errors.Is(err, state.ErrNotFound) {
		row.Activation = productcontrol.CustomerActivation(nil)
	} else if err != nil {
		return row, err
	} else {
		row.Activation = productcontrol.CustomerActivation(&status)
	}
	row.Work, err = work.GetProductCustomerWorkSummary(ctx, acct.ID, app.ID, customer.TenantID)
	if err != nil {
		return row, err
	}
	current, err := products.GetProductCustomer(ctx, acct.ID, app.ID, customer.TenantID)
	if err != nil {
		return row, err
	}
	if current != customer || row.Readiness.Revision != customer.Revision || (status.CurrentCustomer != nil && *status.CurrentCustomer != customer) {
		return row, state.ErrConflict
	}
	row.NextActions = customerOverviewActions(app.Slug, row, period)
	return row, nil
}

func customerOverviewActions(slug string, row api.ProductCustomerOverview, period string) []api.ProductCustomerNextAction {
	base := "/dashboard/apps/" + url.PathEscape(slug) + "/product-control"
	tenant := url.Values{"tenant": {row.Customer.TenantID}}.Encode()
	actions := []api.ProductCustomerNextAction{}
	if row.Readiness.ConfigurationState != "observed" {
		actions = append(actions, api.ProductCustomerNextAction{Code: "review_readiness", Label: "Review readiness checks", URL: base + "/doctor?" + tenant})
	}
	if row.Activation.State != "first_success_observed" {
		actions = append(actions, api.ProductCustomerNextAction{Code: "review_activation", Label: "Review onboarding and activation", URL: base + "/onboarding?" + tenant})
	}
	if row.Work.NeedsReview > 0 {
		actions = append(actions, api.ProductCustomerNextAction{Code: "review_work", Label: "Review unresolved work", URL: base + "?" + tenant + "&period=" + url.QueryEscape(row.Work.ReviewPeriod)})
	}
	if len(actions) == 0 {
		actions = append(actions, api.ProductCustomerNextAction{Code: "review_customer", Label: "Review customer", URL: base + "?" + tenant + "&period=" + url.QueryEscape(period)})
	}
	return actions
}

func customerOverviewView(c *ownerContext, page api.ProductCustomerOverviewPage) *dashboard.ProductControlOverview {
	v := &dashboard.ProductControlOverview{Report: page}
	for _, row := range page.Customers {
		label := "Checks incomplete"
		switch row.Readiness.ConfigurationState {
		case "observed":
			label = "Configuration observed"
		case "blocked":
			label = "Needs configuration"
		}
		v.Customers = append(v.Customers, dashboard.ProductControlOverviewCustomer{ProductCustomerOverview: row, ActivationLabel: productcontrol.CustomerActivationLabel(row.Activation.State), ConfigurationLabel: label, URL: ownerURL(c, row.Customer.TenantID, page.Period, nil)})
	}
	if page.NextCursor != "" {
		v.NextURL = c.data.BaseURL + "/customers?cursor=" + url.QueryEscape(page.NextCursor)
	}
	return v
}
