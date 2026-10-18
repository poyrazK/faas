package product

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) Doctor(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant_id")
	if err != nil {
		respond(w, nil, err)
		return
	}
	value, err := s.doctorReport(r.Context(), acct, app, store, q.Get("tenant_id"))
	respond(w, value, err)
}

func (s *Service) OwnerDoctor(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant")
	if err == nil {
		var report api.ProductDoctorReport
		report, err = s.doctorReport(r.Context(), acct, c.app, c.products, q.Get("tenant"))
		c.data.Doctor = &report
		c.data.TenantID = q.Get("tenant")
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func doctorCheck(name, status, impact, code, message, fix string) api.ProductDoctorCheck {
	return api.ProductDoctorCheck{Check: name, Status: status, Impact: impact, Code: code, Message: message, Remediation: fix}
}

func (s *Service) doctorReport(parent context.Context, acct state.Account, app state.App, store state.ProductControlStore, tenantID string) (api.ProductDoctorReport, error) {
	ctx, cancel := context.WithTimeout(parent, api.ProductControlDoctorMaxDuration)
	defer cancel()
	r := api.ProductDoctorReport{AppID: app.ID, TenantID: tenantID, ObservedAt: time.Now().UTC(), ObservationScope: "responding_api_node", QualificationState: "unverified", Checks: []api.ProductDoctorCheck{}}
	var tenant state.PlatformTenant
	if tenantID != "" {
		if !ownerUUID(tenantID) {
			return r, state.ErrInvalidArgument
		}
		tenants, ok := s.Host.Store().(state.PlatformTenantStore)
		if !ok {
			return r, state.ErrNotFound
		}
		var err error
		tenant, err = tenants.GetPlatformTenant(ctx, acct.ID, tenantID)
		if err != nil {
			return r, err
		}
	}
	head, err := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if errors.Is(err, state.ErrNotFound) {
		r.Checks = append(r.Checks, doctorCheck("catalog", "blocked", "submission", "product_catalog_missing", "This app has no Product Control catalog.", "Open Product Control setup and publish reviewed feature and plan terms."))
	} else if err != nil {
		return r, err
	} else {
		r.HeadRevision, r.Revision = head.Revision, head.Revision
		r.Checks = append(r.Checks, doctorCheck("catalog", "observed", "submission", "product_catalog_observed", "An immutable catalog head is present.", ""))
		s.doctorAuth(ctx, acct, head, &r)
		if err = s.doctorCustomer(ctx, acct, app, store, head, tenant, &r); err != nil {
			return r, err
		}
	}
	r.Checks = append(r.Checks, doctorCheck("customer_login", "unverified", "qualification", "customer_login_unverified", "Live customer login and organization membership were not probed.", "Sign in as each invited internal customer and verify workspace access and revocation."), doctorCheck("native_pilot", "unverified", "qualification", "native_pilot_unverified", "This report does not attest native execution, private downloads or fleet rollback.", "Run the recorded export/interruption trial on an Operations-qualified native host."))
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if len(r.Checks) > api.ProductControlDoctorMaxChecks {
		return r, state.ErrInvalidArgument
	}
	r.ConfigurationState = r.ObservedConfigurationState()
	return r, nil
}

func (s *Service) doctorAuth(ctx context.Context, acct state.Account, head api.ProductCatalog, r *api.ProductDoctorReport) {
	status, code, message, fix := "blocked", "auth_app_missing", "No customer login realm is selected.", "Select an existing account-owned Application Auth app in setup."
	if head.Spec.AuthApp != "" {
		status, code, message, fix = "unverified", "auth_app_unverified", "Application Auth binding could not be observed.", "Restore Auth binding reads and check this app's managed Auth deployment."
		if s.AuthOrigin != nil {
			_, _, err := s.AuthOrigin(ctx, acct, head.Spec.AuthApp)
			if err == nil {
				status, code, message, fix = "configured", "auth_app_configured", "Owned Application Auth binding and platform HTTPS origin are configured; live Auth is unverified.", ""
			} else if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrProductAccess) {
				status, code, message, fix = "blocked", "auth_app_unavailable", "The selected Auth app has no usable owned Auth binding.", "Choose an account-owned Application Auth app and configure its managed Auth binding."
			}
		}
	}
	r.Checks = append(r.Checks, doctorCheck("auth_app", status, "customer_access", code, message, fix))
}

func (s *Service) doctorCustomer(ctx context.Context, acct state.Account, app state.App, store state.ProductControlStore, head api.ProductCatalog, tenant state.PlatformTenant, r *api.ProductDoctorReport) error {
	catalog := head
	var customer *api.ProductCustomer
	var usage api.ProductUsage
	if tenant.ID == "" {
		r.Checks = append(r.Checks, doctorCheck("customer_context", "unverified", "submission", "customer_context_required", "No customer was selected; entitlement, allowance and cohort were not checked.", "Select a customer or use --tenant UUID for customer-scoped checks."))
	} else {
		c, err := store.GetProductCustomer(ctx, acct.ID, app.ID, tenant.ID)
		if errors.Is(err, state.ErrNotFound) {
			r.Checks = append(r.Checks, doctorCheck("assignment", "blocked", "submission", "customer_unassigned", "This tenant has no assigned Product Control plan.", "Assign a reviewed plan from the owner console."))
			return nil
		}
		if err != nil {
			return err
		}
		catalog, err = store.GetProductCatalog(ctx, acct.ID, app.ID, c.Revision)
		if err != nil {
			return err
		}
		customer = &c
		r.Revision = c.Revision
		if len(catalog.Spec.Plans[c.Plan].Features) == 0 {
			r.Checks = append(r.Checks, doctorCheck("feature_access", "blocked", "submission", "plan_has_no_grants", "This plan enables no product features.", "Assign a reviewed plan with the intended feature grant."))
		}
		usage, err = store.GetProductUsage(ctx, acct.ID, app.ID, tenant.ID, time.Now().UTC().Format("2006-01"))
		if err != nil {
			return err
		}
		status, code := "observed", "customer_active"
		if tenant.Status != state.PlatformTenantActive || c.Suspended {
			status, code = "blocked", "customer_suspended"
		}
		r.Checks = append(r.Checks, doctorCheck("assignment", status, "submission", code, "Customer assignment and active status observed.", "Resume the tenant and product access before new starts."))
		status, code = "configured", "organization_link_configured"
		if c.AuthOrganizationID == "" {
			status, code = "blocked", "organization_link_missing"
		}
		r.Checks = append(r.Checks, doctorCheck("organization_link", status, "customer_access", code, "Organization mapping configuration observed; existence and live membership are unverified.", "Link a distinct Auth organization on first assignment; use the existing owner mapping workflow to repair established identities."))
	}
	owner, ok := s.Host.Store().(state.ProductOwnerStore)
	if !ok {
		return state.ErrNotFound
	}
	names := []string{}
	for name := range catalog.Spec.Features {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.doctorFeature(ctx, acct, app, owner, catalog, customer, usage, tenant, name, r)
	}
	return nil
}

func (s *Service) doctorFeature(ctx context.Context, acct state.Account, app state.App, owner state.ProductOwnerStore, catalog api.ProductCatalog, customer *api.ProductCustomer, usage api.ProductUsage, tenant state.PlatformTenant, name string, r *api.ProductDoctorReport) {
	add := func(c api.ProductDoctorCheck) { c.Feature = name; r.Checks = append(r.Checks, c) }
	if customer != nil {
		grant, enabled := catalog.Spec.Plans[customer.Plan].Features[name]
		if !enabled {
			add(doctorCheck("feature_access", "not_requested", "submission", "feature_not_granted", "This plan does not grant this feature.", ""))
			return
		}
		var starts int64
		for _, line := range usage.Lines {
			if line.Feature == name {
				starts += line.Starts
			}
		}
		status, code := "observed", "allowance_available"
		if starts >= grant.MonthlyStarts {
			status, code = "blocked", "allowance_exhausted"
		}
		add(doctorCheck("allowance", status, "submission", code, "Current UTC-month starts were compared with the assigned cap; no start was reserved.", "Review the customer's monthly allowance; changing plans does not reset starts."))
	}
	def, err := owner.InspectProductBinding(ctx, acct.ID, app.ID, catalog.Spec.Features[name])
	if err != nil {
		status := "unverified"
		if errors.Is(err, state.ErrProductBindingUnavailable) {
			status = "blocked"
		}
		add(doctorCheck("binding", status, "submission", "feature_binding_unavailable", "The feature's Operation binding could not be resolved.", "Deploy the named Operation in its explicit environment, or inspect its pinned definition; named bindings require one fully serving deployment."))
		return
	}
	add(doctorCheck("binding", "observed", "submission", "feature_binding_observed", "The feature resolves to an owned immutable Operation definition.", ""))
	if customer == nil {
		return
	}
	if s.OperationDoctor == nil {
		add(doctorCheck("operations", "unverified", "submission", "operations_doctor_unavailable", "Execution configuration could not be observed.", "Configure Operations diagnostics on this API node."))
		return
	}
	observed, err := s.OperationDoctor(ctx, acct, def, tenant)
	if err == nil && (observed.AppID != app.ID || observed.PlatformTenantID != tenant.ID || observed.DeploymentID != def.DeploymentID || len(observed.Checks) == 0) {
		err = state.ErrInvalidArgument
	}
	if err != nil {
		add(doctorCheck("operations", "unverified", "submission", "operations_observation_failed", "Execution prerequisites could not be observed.", "Restore Operations diagnostic reads and repeat this check."))
		return
	}
	for _, c := range observed.Checks {
		status := c.Status
		if status == "unknown" {
			status = "unverified"
		}
		add(doctorCheck("operations/"+c.Check, status, c.Impact, c.Code, c.Message, c.Remediation))
	}
}
