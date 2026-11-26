package product

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

// A batch handles at most one page of the month-end queue.
const ownerBillingBatchMaxWorkspaces = 25

type billingBatchItem struct {
	Tenant string `json:"tenant"`
	Hash   string `json:"hash"`
}

type billingBatchConfirmation struct {
	Period string             `json:"period"`
	Items  []billingBatchItem `json:"items"`
}

func (s *Service) OwnerBillingBatchReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormFields(w, r, []string{"csrf_token", "period", "tenant"}, "tenant", api.ProductControlOwnerFormMaxBytes)
	if err == nil {
		err = s.ownerVerify(r, c, "review", acct.ID+"/"+c.app.ID)
	}
	if err == nil {
		err = s.reviewBillingBatch(w, r.Context(), c, f.Get("period"), f["tenant"])
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) OwnerBillingBatchConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "payload"})
	if err == nil {
		err = s.ownerVerify(r, c, "confirm_billing_batch", ownerConfirmationSubject(c, f.Get("payload")))
	}
	var confirmation billingBatchConfirmation
	if err == nil {
		err = ownerDecodeReviewedPayload(f.Get("payload"), &confirmation)
	}
	if err == nil {
		err = confirmation.validate()
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	c.data.BillingBatch = s.applyBillingBatch(r.Context(), c, confirmation, f.Get("payload"), f.Get("csrf_token"))
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) reviewBillingBatch(w http.ResponseWriter, ctx context.Context, c *ownerContext, period string, tenants []string) error {
	if !ownerClosedBillingPeriod(period) || !validBillingBatchTenants(tenants) {
		return state.ErrInvalidArgument
	}
	view := billingBatchView(c, period)
	confirmation := billingBatchConfirmation{Period: period}
	for _, tenant := range tenants {
		scope := state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, Period: period, TenantID: tenant}
		row, hash, err := previewBillingBatchRow(ctx, c, scope)
		if err != nil {
			return err
		}
		view.Rows = append(view.Rows, row)
		if hash != "" {
			confirmation.Items = append(confirmation.Items, billingBatchItem{Tenant: tenant, Hash: hash})
		}
	}
	view.Ready = len(confirmation.Items)
	if view.Ready > 0 {
		view.Payload = ownerPayload(confirmation)
		token, err := s.ownerToken(w, c, "confirm_billing_batch", ownerConfirmationSubject(c, view.Payload))
		if err != nil {
			return err
		}
		view.Token = token
	}
	c.data.BillingBatch = view
	return nil
}

func previewBillingBatchRow(ctx context.Context, c *ownerContext, scope state.ProductBillingScope) (dashboard.ProductControlBillingBatchRow, string, error) {
	row := dashboard.ProductControlBillingBatchRow{TenantID: scope.TenantID}
	review, err := c.billing.ReviewProductBilling(ctx, scope)
	if errors.Is(err, state.ErrNotFound) || (err == nil && review.Starts == 0 && review.AmountMillicents == 0 && review.Finalized == nil) {
		row.Status = "No statement to finalize for this workspace and month."
		return row, "", nil
	}
	if err != nil {
		return row, "", err
	}
	tenant, err := c.tenants.GetPlatformTenant(ctx, scope.AccountID, scope.TenantID)
	if err != nil {
		return row, "", err
	}
	row.Name = tenant.Name
	var fee int64
	for _, line := range review.Fees {
		fee += line.AmountMillicents
	}
	row.PlanFee = ownerMoney(review.Currency, fee)
	row.RunCharges = ownerMoney(review.Currency, review.AmountMillicents-fee)
	row.Total = ownerMoney(review.Currency, review.AmountMillicents)
	switch {
	case review.Finalized != nil:
		row.Status = "Already finalized"
		row.URL = ownerStatementURL(c, scope, review.Finalized.ID)
	case review.Unresolved > 0:
		row.Status = strconv.FormatInt(review.Unresolved, 10) + " unresolved runs"
		row.URL = ownerReviewRunsURL(c.data.BaseURL, scope.TenantID, scope.Period)
	default:
		row.Status = "Ready to finalize"
		return row, review.ReviewHash, nil
	}
	return row, "", nil
}

func (s *Service) applyBillingBatch(ctx context.Context, c *ownerContext, confirmation billingBatchConfirmation, payload, token string) *dashboard.ProductControlBillingBatch {
	view := billingBatchView(c, confirmation.Period)
	view.Confirmed = true
	for _, item := range confirmation.Items {
		scope := state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, Period: confirmation.Period, TenantID: item.Tenant}
		row := dashboard.ProductControlBillingBatchRow{TenantID: item.Tenant}
		if tenant, err := c.tenants.GetPlatformTenant(ctx, scope.AccountID, item.Tenant); err == nil {
			row.Name = tenant.Name
		}
		// The normal finalizer checks the reviewed hash under the billing locks.
		// A replay with the same hash returns the existing immutable statement.
		doc, err := s.FinalizeStatement(ctx, c.account, scope, item.Hash)
		switch {
		case err == nil:
			row.Status = "Finalized"
			row.Total = ownerMoney(doc.Currency, doc.AmountMillicents)
			row.URL = ownerStatementURL(c, scope, doc.ID)
		case errors.Is(err, state.ErrProductBillingReviewChanged), errors.Is(err, state.ErrConflict):
			row.Status = "Billing changed. Review again."
			row.URL = ownerURL(c, item.Tenant, scope.Period, url.Values{"billing_month": {scope.Period}}) + "#billing"
		case errors.Is(err, state.ErrProductBillingUnresolved):
			row.Status = "Review unresolved runs."
			row.URL = ownerReviewRunsURL(c.data.BaseURL, item.Tenant, scope.Period)
		case errors.Is(err, state.ErrNotFound):
			row.Status = "Workspace or billing is unavailable."
		default:
			row.Status = "Could not confirm the result. Retry this batch."
			view.Payload, view.Token = payload, token
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

func billingBatchView(c *ownerContext, period string) *dashboard.ProductControlBillingBatch {
	monthURL := c.data.BaseURL + "?" + url.Values{"billing_month": {period}}.Encode()
	return &dashboard.ProductControlBillingBatch{Period: period, QueueURL: monthURL + "#month-end", InvoiceURL: ownerInvoiceQueueURL(c, period)}
}

func ownerStatementURL(c *ownerContext, scope state.ProductBillingScope, document string) string {
	params := url.Values{"billing_month": {scope.Period}, "document": {document}}
	return ownerURL(c, scope.TenantID, scope.Period, params) + "#document-" + document
}

func ownerClosedBillingPeriod(period string) bool {
	return productcontrol.ValidatePeriod(period) == nil && period < productcontrol.Period(time.Now())
}

func validBillingBatchTenants(tenants []string) bool {
	if len(tenants) == 0 || len(tenants) > ownerBillingBatchMaxWorkspaces {
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

func (confirmation billingBatchConfirmation) validate() error {
	if !ownerClosedBillingPeriod(confirmation.Period) {
		return state.ErrInvalidArgument
	}
	tenants := make([]string, 0, len(confirmation.Items))
	for _, item := range confirmation.Items {
		if !accessRevision(item.Hash) {
			return state.ErrInvalidArgument
		}
		tenants = append(tenants, item.Tenant)
	}
	if !validBillingBatchTenants(tenants) {
		return state.ErrInvalidArgument
	}
	return nil
}
