package product

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ownerEmailRepository() (productemail.Store, error) {
	store, ok := s.Host.Store().(state.ProductEmailStore)
	if !ok {
		return nil, state.ErrProductBindingUnavailable
	}
	return store.ProductEmailRepository(), nil
}
func (s *Service) ownerEmailScope(ctx context.Context, acct state.Account, app state.App, tenant string) (productemail.OwnerScope, error) {
	scope := productemail.OwnerScope{AccountID: acct.ID, AppID: app.ID, TenantID: tenant}
	if tenant == "" {
		return scope, nil
	}
	if !ownerUUID(tenant) {
		return scope, productemail.ErrInvalid
	}
	store, ok := s.Host.Store().(state.PlatformTenantStore)
	if !ok {
		return scope, state.ErrProductBindingUnavailable
	}
	_, err := store.GetPlatformTenant(ctx, acct.ID, tenant)
	return scope, err
}
func (s *Service) ownerEmailPage(ctx context.Context, acct state.Account, app state.App, tenant, status, cursor string) (api.ProductEmailOwnerPage, error) {
	var page api.ProductEmailOwnerPage
	scope, err := s.ownerEmailScope(ctx, acct, app, tenant)
	if err != nil {
		return page, err
	}
	repo, err := s.ownerEmailRepository()
	if err != nil {
		return page, err
	}
	page, err = repo.OwnerPage(ctx, scope, status, cursor, time.Now().UTC())
	page.Mode = "disabled"
	if s.EmailTransport != nil {
		page.Mode = s.EmailTransport.Mode
	}
	for i := range page.Deliveries {
		d := &page.Deliveries[i]
		if d.RetryEligible && !s.ownerEmailReady() {
			d.RetryEligible = false
			d.RetryBlockedReason = "transport_disabled"
		}
	}
	return page, err
}
func (s *Service) ownerEmailReady() bool {
	return s.Enabled && s.EmailTransport != nil && s.EmailOpen != nil && (s.EmailReady == nil || s.EmailReady())
}
func (s *Service) EmailDeliveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "status", "cursor")
	var page api.ProductEmailOwnerPage
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
		defer cancel()
		page, err = s.ownerEmailPage(ctx, acct, app, q.Get("tenant"), helpStatusFilter(q.Get("status"), "all"), q.Get("cursor"))
	}
	ownerEmailRespond(w, page, err)
}
func (s *Service) EmailRetry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	if _, err := ownerQuery(r); err != nil {
		ownerEmailRespond(w, nil, err)
		return
	}
	var req api.ProductEmailRetryRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlEmailOwnerBodyMaxBytes) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
	defer cancel()
	value, err := s.retryOwnerEmail(ctx, acct, app, r.PathValue("delivery"), req)
	ownerEmailRespond(w, value, err)
}
func (s *Service) retryOwnerEmail(ctx context.Context, acct state.Account, app state.App, id string, req api.ProductEmailRetryRequest) (api.ProductEmailRetryReceipt, error) {
	var receipt api.ProductEmailRetryReceipt
	if !ownerUUID(id) || !ownerUUID(req.TenantID) || req.ExpectedAttempts == nil || *req.ExpectedAttempts < 1 || *req.ExpectedAttempts >= api.ProductControlEmailMaxAttempts {
		return receipt, productemail.ErrInvalid
	}
	scope, err := s.ownerEmailScope(ctx, acct, app, req.TenantID)
	if err != nil {
		return receipt, err
	}
	repo, err := s.ownerEmailRepository()
	if err != nil {
		return receipt, err
	}
	receipt, err = repo.RetryReceipt(ctx, scope, id, *req.ExpectedAttempts, time.Now().UTC())
	if err == nil {
		return receipt, nil
	}
	if !errors.Is(err, productemail.ErrNotFound) {
		return receipt, err
	}
	sub, d, err := repo.OwnerGet(ctx, scope, id, time.Now().UTC())
	if err != nil {
		return receipt, err
	}
	if d.Attempts != *req.ExpectedAttempts || productemail.RetryBlocked(sub, d, time.Now().UTC()) != "" || !s.ownerEmailReady() {
		return receipt, productemail.ErrConflict
	}
	if err = s.checkEmailRetrySource(ctx, sub, d); err != nil {
		return receipt, err
	}
	receipt, err = repo.Retry(ctx, scope, d, time.Now().UTC())
	if err == nil {
		s.Host.EmitAudit(ctx, "product.email.retry", &acct.ID, map[string]any{"app_id": app.ID, "tenant_id": req.TenantID, "delivery_id": id, "failed_attempt": receipt.FailedAttempt})
	}
	return receipt, err
}
func (s *Service) checkEmailRetrySource(ctx context.Context, sub productemail.Subscription, d productemail.Delivery) error {
	recipient, err := s.emailResolve(ctx, sub)
	if err != nil {
		if emailAccessDenied(err) {
			return productemail.ErrConflict
		}
		return state.ErrProductBindingUnavailable
	}
	store, ok := s.Host.Store().(state.ProductNotificationStore)
	if !ok {
		return state.ErrProductBindingUnavailable
	}
	n, err := store.GetProductNotification(ctx, emailScope(sub, recipient.Role), d.NotificationID)
	if emailAccessDenied(err) {
		return productemail.ErrConflict
	}
	if err != nil {
		return err
	}
	if n.Revision != d.Notification.Revision {
		return productemail.ErrConflict
	}
	return nil
}
func ownerEmailError(err error) error {
	switch {
	case errors.Is(err, productemail.ErrInvalid):
		return state.ErrInvalidArgument
	case errors.Is(err, productemail.ErrNotFound):
		return state.ErrNotFound
	case errors.Is(err, productemail.ErrConflict):
		return state.ErrConflict
	default:
		return err
	}
}
func ownerEmailRespond(w http.ResponseWriter, value any, err error) {
	if err == nil {
		raw, e := json.Marshal(value)
		if e != nil {
			err = e
		} else if len(raw)+1 > api.ProductControlEmailOwnerResponseMaxBytes {
			err = state.ErrProductBindingUnavailable
		}
	}
	if errors.Is(err, productemail.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_email_retry_conflict", "Email retry unavailable", "refresh delivery history; retries require a current provider rejection, remaining attempts, unexpired consent and current access"))
		return
	}
	respond(w, value, ownerEmailError(err))
}
