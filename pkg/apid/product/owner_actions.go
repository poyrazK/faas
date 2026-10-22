package product

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func productcontrolOwnerHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func (s *Service) OwnerReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	a, err := ownerParseReview(w, r)
	if err == nil {
		err = s.ownerVerify(r, c, "review", acct.ID+"/"+c.app.ID)
	}
	if err == nil {
		err = s.reviewOwnerAction(r, c, &a)
	}
	if err == nil {
		payload := ownerPayload(a)
		c.data.Confirmation.Payload = payload
		c.data.Confirmation.Token, err = s.ownerToken(w, c, "confirm_"+a.Action, ownerConfirmationSubject(c, payload))
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) OwnerConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "payload"})
	var a ownerAction
	if err == nil {
		a, err = ownerDecodePayload(f.Get("payload"))
	}
	if err == nil {
		err = s.ownerVerify(r, c, "confirm_"+a.Action, ownerConfirmationSubject(c, f.Get("payload")))
	}
	if err == nil {
		err = s.applyOwnerAction(r, c, a)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	c.data.TenantID, c.data.Period = a.Tenant, a.Period
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) ownerFormError(w http.ResponseWriter, r *http.Request, c *ownerContext, err error) {
	if errors.Is(err, middleware.ErrCSRFInvalid) {
		c.data.Confirmation = nil
		c.data.Main = false
		c.data.Error = "This form expired or does not match the reviewed action. Return to the customer and review again."
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		s.OwnerRender(w, r, c.account, c.data)
		return
	}
	s.ownerError(w, r, c, err)
}

func (s *Service) reviewOwnerAction(r *http.Request, c *ownerContext, a *ownerAction) error {
	tenant, err := c.tenants.GetPlatformTenant(r.Context(), c.account.ID, a.Tenant)
	if err != nil {
		return err
	}
	c.data.TenantID, c.data.TenantName, c.data.TenantStatus, c.data.Period = tenant.ID, tenant.Name, string(tenant.Status), a.Period
	confirmation := &dashboard.ProductControlConfirmation{}
	c.data.Confirmation = confirmation
	switch a.Action {
	case "plan":
		return s.reviewOwnerPlan(r, c, a)
	case "finalize":
		review, err := c.billing.ReviewProductBilling(r.Context(), a.scope(c))
		if err != nil {
			return err
		}
		if !review.Closed {
			return state.ErrProductBillingOpenPeriod
		}
		if review.Unresolved != 0 {
			return state.ErrProductBillingUnresolved
		}
		a.Hash = review.ReviewHash
		c.data.Billing = &review
		c.data.BillingAmount = ownerMoney(review.Currency, review.AmountMillicents)
		confirmation.Title = "Finalize statement"
		confirmation.Description = "Freeze this closed month's reviewed charges in one immutable statement. Credits remain separate documents."
		confirmation.Details = []dashboard.ProductControlComparison{{Feature: "Starts", After: strconv.FormatInt(review.Starts, 10)}, {Feature: "Total", After: c.data.BillingAmount}, {Feature: "Review hash", After: review.ReviewHash}}
	case "waive", "credit":
		return s.reviewOwnerReceipt(r, c, a)
	case "handoff":
		doc, err := c.owner.GetProductOwnerDocument(r.Context(), a.scope(c), a.Document)
		if err != nil {
			return err
		}
		confirmation.Title = "Record invoice handoff"
		confirmation.Description = "Record an existing external invoice or credit reference. This does not create an invoice, take payment, or issue a refund. Use the document UUID as the invoicer's idempotency key, and recover its result before retrying an uncertain external request."
		confirmation.Details = []dashboard.ProductControlComparison{{Feature: "Document", After: doc.ID}, {Feature: "Amount", After: ownerMoney(doc.Currency, doc.AmountMillicents)}, {Feature: "External reference", After: a.Reference}}
	}
	return nil
}

func (s *Service) reviewOwnerPlan(r *http.Request, c *ownerContext, a *ownerAction) error {
	p, err := c.owner.PreviewProductCustomerChange(r.Context(), c.account.ID, c.app.ID, a.customer())
	if err != nil {
		return err
	}
	// A historical billing selector never backdates an immediate plan change.
	a.Period, c.data.Period = p.Period, p.Period
	a.ID, a.Hash, a.Revision, a.Organization = uuid.NewString(), p.Hash, p.After.Revision, p.After.AuthOrganizationID
	confirmation := c.data.Confirmation
	confirmation.Title = "Confirm customer plan"
	confirmation.Description = "Apply these terms immediately to future admissions. Monthly starts already used do not reset. Accepted runs retain their original prices and access. This month's fee is the highest of the plans held this month, so a lower fee applies from next month."
	beforePlan, beforeRevision, beforeAccess := "Unassigned", "—", "No access"
	if p.Before != nil {
		beforePlan = p.Before.Plan
		beforeRevision = p.Before.Revision
		beforeAccess = "Enabled"
		if p.Before.Suspended {
			beforeAccess = "Suspended"
		}
	}
	afterAccess := "Enabled"
	if p.After.Suspended {
		afterAccess = "Suspended"
	}
	beforeFee := "—"
	if p.Before != nil && p.BeforeCatalog != nil {
		beforeFee = ownerPlanFee(*p.BeforeCatalog, p.Before.Plan)
	}
	confirmation.Details = []dashboard.ProductControlComparison{{Feature: "Plan", Before: beforePlan, After: p.After.Plan}, {Feature: "Revision", Before: beforeRevision, After: p.After.Revision},
		{Feature: "Monthly fee", Before: beforeFee, After: ownerPlanFee(p.Catalog, p.After.Plan)},
		{Feature: "Free trial", Before: ownerTrial(p.Before, 0), After: ownerTrial(p.Before, p.Catalog.Spec.Plans[p.After.Plan].TrialDays)},
		{Feature: "Product access", Before: beforeAccess, After: afterAccess}, {Feature: "Auth organization (preserved for existing customers)", After: p.After.AuthOrganizationID}}
	names := map[string]bool{}
	for name := range p.Catalog.Spec.Features {
		names[name] = true
	}
	if p.BeforeCatalog != nil {
		for name := range p.BeforeCatalog.Spec.Features {
			names[name] = true
		}
	}
	ordered := []string{}
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	usage, err := c.products.GetProductUsage(r.Context(), c.account.ID, c.app.ID, a.Tenant, p.Period)
	if err != nil {
		return err
	}
	for _, name := range ordered {
		before := "No access"
		if p.Before != nil && p.BeforeCatalog != nil {
			before = ownerGrantDescription(*p.BeforeCatalog, *p.Before, name)
		}
		used := int64(0)
		for _, line := range usage.Lines {
			if line.Feature == name {
				used += line.Starts
			}
		}
		after := ownerGrantDescription(p.Catalog, p.After, name) + "; " + strconv.FormatInt(used, 10) + " starts already used in " + p.Period
		confirmation.Details = append(confirmation.Details, dashboard.ProductControlComparison{Feature: name, Before: before, After: after})
	}
	return nil
}

// ownerTrial describes a customer's trial, or the one an assignment to a plan
// offering days would start (ADR-20261021195901).
func ownerTrial(customer *api.ProductCustomer, days int64) string {
	switch {
	case customer != nil && customer.TrialEndsAt != nil && customer.TrialEndsAt.After(time.Now()):
		return "Until " + customer.TrialEndsAt.UTC().Format(time.DateOnly)
	case customer != nil && customer.TrialEndsAt != nil:
		return "Ended " + customer.TrialEndsAt.UTC().Format(time.DateOnly)
	case days > 0:
		return "Starts now for " + strconv.FormatInt(days, 10) + " days"
	}
	return "None"
}

// ownerPlanFee describes a plan's monthly fee (ADR-20261021154511).
func ownerPlanFee(catalog api.ProductCatalog, plan string) string {
	fee := catalog.Spec.Plans[plan].MonthlyFeeMillicents
	if fee == 0 {
		return "None"
	}
	return ownerMoney(catalog.Spec.Currency, fee) + " / UTC month"
}

func ownerGrantDescription(catalog api.ProductCatalog, customer api.ProductCustomer, feature string) string {
	grant, ok := catalog.Spec.Plans[customer.Plan].Features[feature]
	if !ok || customer.Suspended {
		return "No access"
	}
	price := ownerMoney(catalog.Spec.Currency, grant.UnitPriceMillicents) + " per verified success; "
	if grant.IncludedStarts > 0 {
		// Included starts come first at no charge (ADR-20261021154511).
		price = strconv.FormatInt(grant.IncludedStarts, 10) + " included, then " + price
	}
	return price + strconv.FormatInt(grant.MonthlyStarts, 10) + " starts / UTC month"
}

func (s *Service) reviewOwnerReceipt(r *http.Request, c *ownerContext, a *ownerAction) error {
	snapshot, err := c.products.GetProductReceipt(r.Context(), c.account.ID, c.app.ID, a.Operation)
	if err != nil {
		return err
	}
	e := snapshot.ProductReceiptEvidence
	if e.TenantID != a.Tenant || e.Receipt.Period != a.Period {
		return state.ErrNotFound
	}
	confirmation := c.data.Confirmation
	confirmation.Details = []dashboard.ProductControlComparison{{Feature: "Operation", After: e.OperationID}, {Feature: "Original unit price", After: ownerMoney(e.Receipt.Currency, e.Receipt.UnitPriceMillicents)}, {Feature: "Reason", After: a.Reason}}
	if a.Action == "waive" {
		if e.ResolvedState != "" && e.WaivedAt == nil {
			return state.ErrConflict
		}
		confirmation.Title = "Permanently waive this charge"
		confirmation.Description = "Forgo this run's charge, including any later verified success. This does not cancel or restart the run, prove an outcome, or restore its monthly allowance."
		return nil
	}
	amount, _ := strconv.ParseInt(a.Amount, 10, 64)
	if e.ResolvedState != api.OperationSucceeded || e.WaivedAt != nil || amount > e.Receipt.UnitPriceMillicents {
		return state.ErrConflict
	}
	review, err := c.billing.ReviewProductBilling(r.Context(), a.scope(c))
	if err != nil {
		return err
	}
	if review.Finalized == nil {
		return state.ErrConflict
	}
	a.ID = uuid.NewString()
	confirmation.Title = "Issue credit note"
	confirmation.Description = "Create a separate credit against this finalized statement. The remaining credit balance is rechecked when you confirm. This does not move money or refund a payment."
	confirmation.Details = append(confirmation.Details, dashboard.ProductControlComparison{Feature: "Credit", After: ownerMoney(e.Receipt.Currency, -amount)}, dashboard.ProductControlComparison{Feature: "Stable credit ID", After: a.ID}, dashboard.ProductControlComparison{Feature: "Statement", After: review.Finalized.ID})
	return nil
}

func (s *Service) applyOwnerAction(r *http.Request, c *ownerContext, a ownerAction) error {
	scope := a.scope(c)
	var id string
	var err error
	switch a.Action {
	case "plan":
		var receipt api.ProductCustomerChangeReceipt
		receipt, err = c.owner.ApplyProductCustomerChange(r.Context(), c.account.ID, c.app.ID, api.ProductCustomerChangeRequest{ID: a.ID, ExpectedHash: a.Hash, Customer: a.customer()})
		id = receipt.ID
		if err == nil {
			c.data.Notice = "Customer plan confirmed. Change " + id + " took effect at " + receipt.EffectiveAt.UTC().Format(time.RFC3339) + ". Retrying this confirmation returns this same receipt and cannot overwrite a later change."
		}
	case "finalize":
		var doc api.ProductBillingDocument
		doc, err = c.billing.FinalizeProductBilling(r.Context(), scope, a.Hash)
		id = doc.ID
	case "waive":
		var receipt api.ProductReceiptEvidence
		receipt, err = c.billing.WaiveProductReceipt(r.Context(), scope, a.Operation, a.Reason)
		id = receipt.OperationID
	case "credit":
		var doc api.ProductBillingDocument
		amount, _ := strconv.ParseInt(a.Amount, 10, 64)
		doc, err = c.billing.CreditProductReceipt(r.Context(), scope, api.ProductCreditRequest{ID: a.ID, TenantID: a.Tenant, OperationID: a.Operation, AmountMillicents: amount, Reason: a.Reason})
		id = doc.ID
	case "handoff":
		var handoff api.ProductInvoiceHandoff
		handoff, err = s.handoffManually(r.Context(), c.billing, c.account.ID, scope, a.Document, a.Reference)
		id = handoff.DocumentID
	default:
		return state.ErrInvalidArgument
	}
	if err == nil {
		if c.data.Notice == "" {
			c.data.Notice = "Confirmed " + a.Action + ". Retained reference: " + id + ". Retrying this confirmation returns the retained result."
		}
		s.Host.EmitAudit(r.Context(), "product.owner."+a.Action, &c.account.ID, map[string]any{"app_id": c.app.ID, "tenant_id": a.Tenant, "period": a.Period, "reference_id": id})
	}
	return err
}
