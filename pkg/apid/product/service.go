// Package product implements the internal Product Control API and customer portal.
package product

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

type Host interface {
	httpkit.AppLoader
	Store() state.Store
	EmitAudit(context.Context, string, *string, map[string]any)
}

type Service struct {
	EmailTransport     *productemail.Transport
	EmailSeal          func(string, string) ([]byte, error)
	EmailOpen          func(string, []byte) (string, error)
	EmailOrigin        string
	EmailReady         func() bool
	EmailWebhookSecret string
	Host               Host
	Enabled            bool
	StartOperation     func(http.ResponseWriter, *http.Request, state.Account, api.ProductStartRequest)
	AuthOrigin         func(context.Context, state.Account, string) (state.App, string, error)
	AuthClient         *http.Client
	OwnerSessions      func() *session.Manager
	OwnerRender        func(http.ResponseWriter, *http.Request, state.Account, dashboard.ProductControlData)
	OperationDoctor    func(context.Context, state.Account, state.OperationDefinition, state.PlatformTenant) (api.OperationDoctorResponse, error)
	// ExportInvoice and InvoiceExport reach statement invoice exports
	// (ADR-20261018212918): document, provider and customer, or document.
	ExportInvoice func(context.Context, state.Account, state.ProductBillingScope, string, string, string) (api.StatementInvoiceExportResponse, *api.Problem)
	InvoiceExport func(context.Context, state.Account, state.ProductBillingScope, string) (api.StatementInvoiceExportResponse, *api.Problem)
	// DocumentInvoice reads a scoped statement's export by account and
	// document, nil before one starts.
	DocumentInvoice func(context.Context, string, string) (*api.StatementInvoiceExportResponse, error)
	// Now pins the overdue recomputation clock in tests.
	Now func() time.Time
	// BillingDetails and PutBillingDetails reach a customer's billing details
	// in the owner's Paddle by provider customer ID (ADR-20261020120000);
	// the put also returns the parts it changed.
	BillingDetails    func(context.Context, state.Account, string) (api.ProductBillingDetails, *api.Problem)
	PutBillingDetails func(context.Context, state.Account, string, api.PutProductBillingDetailsRequest) (api.ProductBillingDetails, []string, *api.Problem)
}

func (s *Service) Start(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductStartRequest
	if !operation.DecodeOperationBody(w, r, &req, api.OperationSubmissionMaxBytes+api.OperationStartBodyOverheadBytes) {
		return
	}
	if req.Feature == "" {
		api.WriteProblem(w, api.ErrValidation("feature is required"))
		return
	}
	if req.App != "" {
		var ok bool
		req, ok = s.resolveStart(w, r, acct, req)
		if !ok {
			return
		}
	} else if req.DefinitionID == "" {
		api.WriteProblem(w, api.ErrValidation("app and feature are required"))
		return
	}
	s.StartOperation(w, r, acct, req)
}

func (s *Service) resolveStart(w http.ResponseWriter, r *http.Request, acct state.Account, req api.ProductStartRequest) (api.ProductStartRequest, bool) {
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return req, false
	}
	r.SetPathValue("slug", req.App)
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return req, false
	}
	if r.Header.Get("Idempotency-Key") == "" {
		api.WriteProblem(w, api.ErrValidation("a stable Idempotency-Key is required"))
		return req, false
	}
	def, err := store.ResolveProductFeature(r.Context(), state.ProductFeatureRequest{AccountID: acct.ID, AppID: app.ID, TenantID: tenant, Feature: req.Feature, IdempotencyKey: r.Header.Get("Idempotency-Key"), Input: req.Input})
	if err != nil {
		respond(w, nil, err)
		return req, false
	}
	if req.DefinitionID != "" && req.DefinitionID != def.ID {
		respond(w, nil, state.ErrProductBindingUnavailable)
		return req, false
	}
	req.DefinitionID = def.ID
	return req, true
}

// Gate wraps authentication too, keeping the entire internal surface absent
// while disabled. It does not enable Operations preview admission.
func (s *Service) Gate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.Enabled {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "the requested resource is unavailable"))
			return
		}
		next(w, r)
	}
}

func (s *Service) owned(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductControlStore, state.App, bool) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, state.App{}, false
	}
	store, ok := s.Host.Store().(state.ProductControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Product Control storage is unavailable"))
		return nil, state.App{}, false
	}
	return store, app, true
}

func (s *Service) GetCatalog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	catalog, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, r.URL.Query().Get("revision"))
	respond(w, catalog, err)
}

func (s *Service) PutCatalog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	var spec api.ProductCatalogSpec
	if !operation.DecodeOperationBody(w, r, &spec, api.ProductControlMaxCatalogBytes) {
		return
	}
	if spec.AuthApp != "" {
		if s.AuthOrigin == nil {
			respond(w, nil, state.ErrProductAccess)
			return
		}
		if _, _, err := s.AuthOrigin(r.Context(), acct, spec.AuthApp); err != nil {
			respond(w, nil, err)
			return
		}
	}
	catalog, err := store.PutProductCatalog(r.Context(), acct.ID, app.ID, spec)
	if err == nil {
		s.Host.EmitAudit(r.Context(), "product.catalog.published", &acct.ID, map[string]any{"app_id": app.ID, "revision": catalog.Revision})
	}
	respond(w, catalog, err)
}

func (s *Service) SetCustomer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	var req struct {
		AuthOrganizationID string `json:"auth_organization_id"`
		Plan               string `json:"plan"`
		Revision           string `json:"revision"`
		Suspended          bool   `json:"suspended"`
	}
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlMaxCatalogBytes) {
		return
	}
	customer, err := store.SetProductCustomer(r.Context(), acct.ID, app.ID, api.ProductCustomer{TenantID: r.PathValue("tenant"), Plan: req.Plan, Revision: req.Revision, Suspended: req.Suspended, AuthOrganizationID: req.AuthOrganizationID})
	if err == nil {
		s.Host.EmitAudit(r.Context(), "product.customer.assigned", &acct.ID, map[string]any{"app_id": app.ID, "tenant_id": customer.TenantID, "revision": customer.Revision, "plan": customer.Plan, "suspended": customer.Suspended})
	}
	respond(w, customer, err)
}

func (s *Service) ListCustomers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	customers, err := store.ListProductCustomers(r.Context(), acct.ID, app.ID, r.URL.Query().Get("cursor"))
	respond(w, customers, err)
}

func requestPeriod(r *http.Request) string {
	if period := r.URL.Query().Get("period"); period != "" {
		return period
	}
	return productcontrol.Period(time.Now())
}

func (s *Service) Usage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	usage, err := store.GetProductUsage(r.Context(), acct.ID, app.ID, r.URL.Query().Get("tenant_id"), requestPeriod(r))
	respond(w, usage, err)
}

func (s *Service) Portal(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return
	}
	r.SetPathValue("slug", r.URL.Query().Get("app"))
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	var access *api.ProductCustomerAccess
	if identity, customer := authmw.ProductCustomerFromContext(r.Context()); customer {
		access = &identity.Access
	}
	view, err := customerView(r.Context(), store, acct.ID, app.ID, tenant, requestPeriod(r), access)
	respond(w, view, err)
}

func customerView(ctx context.Context, store state.ProductControlStore, account, appID, tenant, period string, access *api.ProductCustomerAccess) (api.ProductPortal, error) {
	return customerViewWithHistory(ctx, store, account, appID, tenant, period, access, true)
}

func customerViewWithHistory(parent context.Context, store state.ProductControlStore, account, appID, tenant, period string, access *api.ProductCustomerAccess, history bool) (api.ProductPortal, error) {
	ctx, cancel := context.WithTimeout(parent, api.ProductControlGuidanceMaxDuration)
	defer cancel()
	observed := time.Now().UTC()
	customer, err := store.GetProductCustomer(ctx, account, appID, tenant)
	if err != nil {
		return api.ProductPortal{}, err
	}
	catalog, err := store.GetProductCatalog(ctx, account, appID, customer.Revision)
	if err != nil {
		return api.ProductPortal{}, err
	}
	permissions := api.ProductPermissions{ViewWork: true, RunWork: true, ViewBilling: true}
	if access != nil {
		permissions = access.Permissions
	}
	plan := catalog.Spec.Plans[customer.Plan]
	features := map[string]api.ProductFeature{}
	for name := range plan.Features {
		features[name] = catalog.Spec.Features[name]
	}
	catalog.Spec.Features, catalog.Spec.Plans = features, map[string]api.ProductPlan{customer.Plan: plan}
	view := api.ProductPortal{Access: access, AccountID: account, AppID: appID, Customer: customer, Catalog: catalog, Operations: []api.OperationSummary{}, Bindings: map[string]api.ProductResolvedBinding{}, Quotes: map[string]string{}, Allowances: map[string]int64{}}
	if err := populateCustomerView(ctx, store, &view, permissions, period, observed, history); err != nil {
		return api.ProductPortal{}, err
	}
	return view, nil
}

func populateCustomerView(ctx context.Context, store state.ProductControlStore, view *api.ProductPortal, permissions api.ProductPermissions, period string, observed time.Time, history bool) error {
	var err error
	var usage *api.ProductUsage
	if permissions.ViewWork && history {
		view.Operations, err = productHistory(ctx, store, view.AccountID, view.AppID, view.Customer.TenantID, view.Catalog)
		if err != nil {
			return err
		}
	}
	if permissions.RunWork || permissions.ViewBilling {
		value, err := store.GetProductUsage(ctx, view.AccountID, view.AppID, view.Customer.TenantID, period)
		if err != nil {
			return err
		}
		if permissions.ViewBilling {
			view.Usage = &value
		}
		usage = &value
		for name, grant := range view.Catalog.Spec.Plans[view.Customer.Plan].Features {
			remaining := grant.MonthlyStarts
			for _, line := range value.Lines {
				if line.Feature == name {
					remaining -= line.Starts
				}
			}
			view.Allowances[name] = max(0, remaining)
		}
	}
	if permissions.RunWork || permissions.ViewBilling {
		if view.OverdueHold, err = customerOverdueHold(ctx, store, view.AccountID, view.AppID, view.Customer.TenantID); err != nil {
			return err
		}
	}
	if permissions.RunWork {
		view.Bindings, err = productBindings(ctx, store, view.AccountID, view.AppID, view.Customer.TenantID, view.Catalog)
		if err != nil {
			return err
		}
		for name := range view.Catalog.Spec.Features {
			view.Quotes[name] = productcontrol.Quote(view.AccountID, view.AppID, view.Customer.TenantID, name, view.Catalog, view.Customer, period)
		}
	}
	if period == productcontrol.Period(observed) {
		if err := populateCustomerGuidance(ctx, store, view, permissions, usage, observed); err != nil {
			return err
		}
	}
	if !permissions.RunWork {
		view.Catalog.Spec.Features = map[string]api.ProductFeature{}
		plan := view.Catalog.Spec.Plans[view.Customer.Plan]
		plan.Features = map[string]api.ProductFeatureGrant{}
		view.Catalog.Spec.Plans[view.Customer.Plan] = plan
	}
	view.Catalog.Spec.Access = nil
	view.Catalog.Spec.SelfService = nil
	view.Catalog.Spec.Team = nil
	return ctx.Err()
}

func productBindings(ctx context.Context, store state.ProductControlStore, account, appID, tenant string, catalog api.ProductCatalog) (map[string]api.ProductResolvedBinding, error) {
	bindings := make(map[string]api.ProductResolvedBinding, len(catalog.Spec.Features))
	for name := range catalog.Spec.Features {
		def, err := store.ResolveProductFeature(ctx, state.ProductFeatureRequest{AccountID: account, AppID: appID, TenantID: tenant, Feature: name})
		switch {
		case errors.Is(err, state.ErrProductBindingUnavailable), errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrProductAccess):
			bindings[name] = api.ProductResolvedBinding{Reason: "operation_binding_unavailable"}
		case err != nil:
			return nil, err
		default:
			bindings[name] = api.ProductResolvedBinding{Available: true, DefinitionID: def.ID, DefinitionRevision: def.Revision, DeploymentID: def.DeploymentID, InputSchema: def.Spec.InputSchema}
		}
	}
	return bindings, nil
}

func productHistory(ctx context.Context, store state.ProductControlStore, account, appID, tenant string, catalog api.ProductCatalog) ([]api.OperationSummary, error) {
	operations, ok := store.(state.OperationStore)
	if !ok {
		return nil, state.ErrNotFound
	}
	unique := map[string]api.OperationSummary{}
	for _, feature := range catalog.Spec.Features {
		var binding api.ProductOperationBinding
		if feature.Operation != nil {
			binding = *feature.Operation
		} else {
			def, err := operations.OperationDefinitionByID(ctx, account, feature.DefinitionID)
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			binding = api.ProductOperationBinding{Name: def.Spec.Name, Scope: def.Scope}
		}
		page, err := operations.ListPlatformTenantOperations(ctx, account, tenant, api.OperationListOptions{AppID: appID, Scope: binding.Scope, Name: binding.Name, Limit: api.OperationHistoryPageDefault})
		if err != nil {
			return nil, err
		}
		for _, item := range page.Operations {
			unique[item.ID] = item
		}
	}
	rows := make([]api.OperationSummary, 0, len(unique))
	for _, item := range unique {
		rows = append(rows, item)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if len(rows) > api.OperationHistoryPageDefault {
		rows = rows[:api.OperationHistoryPageDefault]
	}
	return rows, nil
}

func respond(w http.ResponseWriter, value any, err error) {
	switch {
	case err == nil:
		httpkit.WriteJSON(w, http.StatusOK, value)
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Product unavailable", "no product catalog or customer plan is available"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid product catalog, customer assignment or usage selector"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_configuration_conflict", "Product configuration conflict", "the requested change conflicts with the retained product configuration"))
	case errors.Is(err, state.ErrProductBindingUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_operation_binding_unavailable", "Feature temporarily unavailable", "the feature requires its named Operation on one fully serving deployment; retry with the same key after deployment completes"))
	default:
		operation.WriteOperationError(w, err)
	}
}
