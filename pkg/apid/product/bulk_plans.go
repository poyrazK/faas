package product

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

const ownerBulkPlanMaxCustomers = 10

type bulkPlanSelection struct {
	Plan   string `json:"plan"`
	Source string `json:"source"`
	Target string `json:"target"`
}

func (s bulkPlanSelection) valid() bool {
	return productcontrol.ValidProductPlanName(s.Plan) && accessRevision(s.Source) && accessRevision(s.Target) && s.Source != s.Target
}

func (s bulkPlanSelection) matches(customer api.ProductCustomer) bool {
	return customer.Plan == s.Plan && customer.Revision == s.Source
}

type bulkPlanChange struct {
	Tenant    string `json:"tenant"`
	ID        string `json:"id"`
	Hash      string `json:"hash"`
	Suspended bool   `json:"suspended,omitempty"`
}

type bulkPlanConfirmation struct {
	Selection bulkPlanSelection `json:"selection"`
	Changes   []bulkPlanChange  `json:"changes"`
}

func (s *Service) OwnerBulkPlans(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "cursor")
	if err == nil {
		err = s.loadBulkPlans(w, r, c, q.Get("cursor"))
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) loadBulkPlans(w http.ResponseWriter, r *http.Request, c *ownerContext, cursor string) error {
	if cursor != "" && !ownerUUID(cursor) {
		return state.ErrInvalidArgument
	}
	ctx := r.Context()
	head, err := c.products.GetProductCatalog(ctx, c.account.ID, c.app.ID, "")
	if err != nil {
		return err
	}
	page, err := c.products.ListProductCustomers(ctx, c.account.ID, c.app.ID, cursor)
	if err != nil {
		return err
	}
	view := &dashboard.ProductControlBulkPlans{Target: head.Revision, MaxCustomers: ownerBulkPlanMaxCustomers}
	if page.NextCursor != "" {
		view.NextURL = c.data.BaseURL + "/bulk-plans?" + url.Values{"cursor": {page.NextCursor}}.Encode()
	}
	groups := map[bulkPlanSelection]int{}
	for _, customer := range page.Customers {
		plan, ok := head.Spec.Plans[customer.Plan]
		if !ok || customer.Revision == head.Revision {
			continue
		}
		tenant, err := c.tenants.GetPlatformTenant(ctx, c.account.ID, customer.TenantID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		selection := bulkPlanSelection{Plan: customer.Plan, Source: customer.Revision, Target: head.Revision}
		index, exists := groups[selection]
		if !exists {
			index = len(view.Groups)
			groups[selection] = index
			view.Groups = append(view.Groups, dashboard.ProductControlBulkPlanGroup{Plan: selection.Plan, Label: plan.Label, Source: selection.Source})
		}
		view.Groups[index].Customers = append(view.Groups[index].Customers, dashboard.ProductControlCustomer{
			TenantID: tenant.ID, Name: tenant.Name, Suspended: customer.Suspended,
		})
	}
	if len(view.Groups) > 0 {
		view.Token, err = s.ownerToken(w, c, "bulk_plans_review", c.account.ID+"/"+c.app.ID)
		if err != nil {
			return err
		}
	}
	c.data.BulkPlans = view
	return nil
}

func (s *Service) OwnerBulkPlansReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormFields(w, r, []string{"csrf_token", "plan", "source", "target", "tenant"}, "tenant", api.ProductControlOwnerFormMaxBytes)
	if err == nil {
		err = s.ownerVerify(r, c, "bulk_plans_review", acct.ID+"/"+c.app.ID)
	}
	if err == nil {
		selection := bulkPlanSelection{Plan: f.Get("plan"), Source: f.Get("source"), Target: f.Get("target")}
		err = s.reviewBulkPlans(w, r.Context(), c, selection, f["tenant"])
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) reviewBulkPlans(w http.ResponseWriter, ctx context.Context, c *ownerContext, selection bulkPlanSelection, tenants []string) error {
	if !selection.valid() || !validBulkPlanTenants(tenants) {
		return state.ErrInvalidArgument
	}
	head, err := c.products.GetProductCatalog(ctx, c.account.ID, c.app.ID, "")
	if err != nil {
		return err
	}
	if _, ok := head.Spec.Plans[selection.Plan]; !ok || head.Revision != selection.Target {
		return state.ErrProductCatalogStale
	}
	view := &dashboard.ProductControlBulkPlans{Plan: selection.Plan, Source: selection.Source, Target: selection.Target}
	confirmation := bulkPlanConfirmation{Selection: selection}
	sorted := append([]string(nil), tenants...)
	sort.Strings(sorted)
	for _, tenant := range sorted {
		row, change, err := previewBulkPlanCustomer(ctx, c, selection, tenant)
		if err != nil {
			return err
		}
		view.Reviews = append(view.Reviews, row)
		if change.ID != "" {
			confirmation.Changes = append(confirmation.Changes, change)
		}
	}
	view.ConfirmCount = len(confirmation.Changes)
	if view.ConfirmCount > 0 {
		view.Payload = encodeBulkPlanConfirmation(confirmation)
		view.Token, err = s.ownerToken(w, c, "bulk_plans_confirm", ownerConfirmationSubject(c, view.Payload))
		if err != nil {
			return err
		}
	}
	c.data.BulkPlans = view
	return nil
}

func previewBulkPlanCustomer(ctx context.Context, c *ownerContext, selection bulkPlanSelection, tenantID string) (dashboard.ProductControlBulkPlanRow, bulkPlanChange, error) {
	row := dashboard.ProductControlBulkPlanRow{TenantID: tenantID}
	var change bulkPlanChange
	current, err := c.products.GetProductCustomer(ctx, c.account.ID, c.app.ID, tenantID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return row, change, err
	}
	if !selection.matches(current) {
		row.Message = "Skipped. This workspace is no longer on the selected plan and revision."
		return row, change, nil
	}
	tenant, err := c.tenants.GetPlatformTenant(ctx, c.account.ID, tenantID)
	if errors.Is(err, state.ErrNotFound) {
		row.Message = "Skipped. This workspace is unavailable."
		return row, change, nil
	}
	if err != nil {
		return row, change, err
	}
	row.Name = tenant.Name
	target := api.ProductCustomer{TenantID: tenantID, Plan: selection.Plan, Revision: selection.Target, Suspended: current.Suspended}
	preview, err := c.owner.PreviewProductCustomerChange(ctx, c.account.ID, c.app.ID, target)
	needsReview := bulkPlanNeedsReview(err)
	if err != nil && !needsReview {
		return row, change, err
	}
	if needsReview || preview.Before == nil || !selection.matches(*preview.Before) || preview.Before.Suspended != target.Suspended || preview.HeadRevision != selection.Target {
		row.Message = "Skipped. This workspace or the current catalog changed during review."
		return row, change, nil
	}
	if preview.BeforeCatalog == nil || preview.Pricing == nil {
		return row, change, state.ErrConflict
	}
	proposed := preview.Catalog.Spec.Plans[preview.After.Plan]
	row.Details = []dashboard.ProductControlComparison{
		{Feature: "Recurring plan fee", Before: ownerPlanFee(*preview.BeforeCatalog, selection.Plan), After: ownerPlanFee(preview.Catalog, selection.Plan)},
		{Feature: "Free trial", Before: ownerTrial(preview.Before, 0), After: ownerTrial(preview.Before, proposed.TrialDays)},
	}
	if proposed.RequiresPaymentMethod {
		row.Details = append(row.Details, dashboard.ProductControlComparison{Feature: "After the trial", After: ownerTrialConversion(proposed, preview.Before)})
	}
	row.Details = append(row.Details, ownerPlanPricingDetails(preview)...)
	row.Warnings = bulkPlanWarnings(preview)
	change = bulkPlanChange{Tenant: tenantID, ID: uuid.NewString(), Hash: preview.Hash, Suspended: target.Suspended}
	return row, change, nil
}

func bulkPlanWarnings(preview api.ProductCustomerChangePreview) []string {
	if preview.After.Suspended {
		return []string{"Product access is suspended for this workspace."}
	}
	current := preview.BeforeCatalog.Spec.Plans[preview.Before.Plan]
	proposed := preview.Catalog.Spec.Plans[preview.After.Plan]
	var warnings []string
	for _, run := range preview.Pricing.Runs {
		_, hadFeature := current.Features[run.Feature]
		grant, hasFeature := proposed.Features[run.Feature]
		if hadFeature && !hasFeature {
			warnings = append(warnings, "Feature access removed for "+run.Feature+".")
		}
		if hasFeature && run.Starts >= grant.MonthlyStarts {
			warnings = append(warnings, "No starts left under the proposed allowance for "+run.Feature+".")
		}
	}
	return warnings
}

func (s *Service) OwnerBulkPlansConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "payload"})
	if err == nil {
		err = s.ownerVerify(r, c, "bulk_plans_confirm", ownerConfirmationSubject(c, f.Get("payload")))
	}
	var confirmation bulkPlanConfirmation
	if err == nil {
		confirmation, err = decodeBulkPlanConfirmation(f.Get("payload"))
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	c.data.BulkPlans = s.applyBulkPlans(r.Context(), c, confirmation, f.Get("payload"), f.Get("csrf_token"))
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) applyBulkPlans(ctx context.Context, c *ownerContext, confirmation bulkPlanConfirmation, payload, token string) *dashboard.ProductControlBulkPlans {
	sel := confirmation.Selection
	view := &dashboard.ProductControlBulkPlans{Plan: sel.Plan, Source: sel.Source, Target: sel.Target}
	for _, change := range confirmation.Changes {
		row := dashboard.ProductControlBulkPlanRow{TenantID: change.Tenant}
		if tenant, err := c.tenants.GetPlatformTenant(ctx, c.account.ID, change.Tenant); err == nil {
			row.Name = tenant.Name
		}
		receipt, err := c.owner.ApplyProductCustomerChange(ctx, c.account.ID, c.app.ID, api.ProductCustomerChangeRequest{
			ID: change.ID, ExpectedHash: change.Hash,
			Customer: api.ProductCustomer{TenantID: change.Tenant, Plan: sel.Plan, Revision: sel.Target, Suspended: change.Suspended},
		})
		switch {
		case err == nil:
			row.Message = "Applied"
			row.ReceiptID, row.EffectiveAt = receipt.ID, receipt.EffectiveAt.UTC().Format(time.RFC3339)
			s.Host.EmitAudit(ctx, "product.owner.plan", &c.account.ID, map[string]any{
				"app_id": c.app.ID, "tenant_id": change.Tenant, "period": productcontrol.Period(receipt.EffectiveAt),
				"reference_id": receipt.ID, "source": "bulk_plan_revision",
			})
		case bulkPlanNeedsReview(err):
			row.Message = "Skipped. Review this workspace again."
		default:
			row.Message = "Confirmation unavailable. Retry this review to check the outcome."
			view.Payload, view.Token = payload, token
		}
		view.Results = append(view.Results, row)
	}
	return view
}

func bulkPlanNeedsReview(err error) bool {
	return errors.Is(err, state.ErrProductCustomerChangeStale) || errors.Is(err, state.ErrProductCatalogStale) || errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound)
}

func validBulkPlanTenants(tenants []string) bool {
	if len(tenants) == 0 || len(tenants) > ownerBulkPlanMaxCustomers {
		return false
	}
	seen := make(map[string]bool, len(tenants))
	for _, tenant := range tenants {
		if !ownerUUID(tenant) || seen[tenant] {
			return false
		}
		seen[tenant] = true
	}
	return true
}

func encodeBulkPlanConfirmation(confirmation bulkPlanConfirmation) string {
	raw, _ := json.Marshal(confirmation)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeBulkPlanConfirmation(payload string) (bulkPlanConfirmation, error) {
	var confirmation bulkPlanConfirmation
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return confirmation, state.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&confirmation) != nil || d.Decode(new(any)) != io.EOF || encodeBulkPlanConfirmation(confirmation) != payload || !confirmation.Selection.valid() {
		return confirmation, state.ErrInvalidArgument
	}
	tenants := make([]string, 0, len(confirmation.Changes))
	for _, change := range confirmation.Changes {
		if !ownerUUID(change.ID) || !accessRevision(change.Hash) {
			return confirmation, state.ErrInvalidArgument
		}
		tenants = append(tenants, change.Tenant)
	}
	if !validBulkPlanTenants(tenants) {
		return confirmation, state.ErrInvalidArgument
	}
	return confirmation, nil
}
