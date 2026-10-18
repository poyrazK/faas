package product

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) CustomerEmailRecover(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if _, err := ownerQuery(r); err != nil {
		emailRecoveryRespond(w, nil, err)
		return
	}
	var req api.ProductEmailRecoveryRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlEmailOwnerBodyMaxBytes) {
		return
	}
	if !productemail.ValidRecoveryRequest(req) {
		emailRecoveryRespond(w, nil, productemail.ErrInvalid)
		return
	}
	c, err := s.emailCustomer(r)
	if err != nil {
		customerError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
	defer cancel()
	var value api.ProductEmailRecoveryReceipt
	err = c.store.WithConsent(ctx, c.key, func() error { var e error; value, e = s.recoverCustomerEmail(ctx, c, req); return e })
	emailRecoveryRespond(w, value, err)
}
func (s *Service) recoverCustomerEmail(ctx context.Context, c emailCustomer, req api.ProductEmailRecoveryRequest) (api.ProductEmailRecoveryReceipt, error) {
	var out api.ProductEmailRecoveryReceipt
	out, err := c.store.RecoveryReceipt(ctx, c.key, req.ID, time.Now().UTC())
	if err == nil {
		if !productemail.MatchesRecovery(out, req) {
			return out, productemail.ErrConflict
		}
		return out, nil
	}
	if !errors.Is(err, productemail.ErrNotFound) {
		return out, err
	}
	old, err := c.store.Get(ctx, c.key)
	if err != nil {
		return out, err
	}
	if old.Version != req.ExpectedVersion || old.Generation != req.ExpectedGeneration || productemail.RecoveryBlocked(old) != "verified_replacement_required" || !s.emailPreferences(old).Available {
		return out, productemail.ErrConflict
	}
	previous, err := s.EmailOpen(productemail.RecipientEvidenceID(c.key), old.PauseRecipient)
	if err != nil {
		return out, state.ErrProductBindingUnavailable
	}
	next := old
	next.Work, next.Help = *req.WorkOutcomes, *req.HelpReplies
	next, err = s.emailConsent(ctx, c, next)
	if err != nil {
		return out, err
	}
	recipient, err := s.emailResolve(ctx, next)
	if err != nil {
		return out, err
	}
	store, ok := s.Host.Store().(state.ProductNotificationStore)
	if !ok {
		return out, state.ErrProductBindingUnavailable
	}
	if _, err = store.ListProductNotifications(ctx, emailScope(next, recipient.Role), "all", ""); err != nil {
		return out, err
	}
	if previous == productemail.RecipientEvidence(recipient.Email) {
		return out, productemail.ErrConflict
	}
	out, err = c.store.Recover(ctx, next, req, time.Now().UTC())
	if err == nil {
		s.Host.EmitAudit(ctx, "product.email.recovered", &c.key.AccountID, map[string]any{"app_id": c.key.AppID, "tenant_id": c.key.TenantID, "recipient_id": c.key.ID(), "recovery_id": out.ID, "generation": out.Generation})
	}
	return out, err
}
func (s *Service) CustomerEmailRecoveries(w http.ResponseWriter, r *http.Request, _ state.Account) {
	q, err := ownerQuery(r, "cursor")
	if err != nil {
		emailRecoveryRespond(w, nil, err)
		return
	}
	c, err := s.emailCustomer(r)
	if err != nil {
		customerError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
	defer cancel()
	scope := productemail.OwnerScope{AccountID: c.key.AccountID, AppID: c.key.AppID, TenantID: c.key.TenantID}
	value, err := c.store.Recoveries(ctx, scope, c.key.ID(), q.Get("cursor"), time.Now().UTC())
	emailRecoveryRespond(w, value, err)
}
func (s *Service) EmailRecoveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "cursor")
	var page api.ProductEmailRecoveryPage
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
		defer cancel()
		page, err = s.ownerEmailRecoveries(ctx, acct, app, q.Get("tenant"), q.Get("cursor"))
	}
	emailRecoveryRespond(w, page, err)
}
func (s *Service) ownerEmailRecoveries(ctx context.Context, acct state.Account, app state.App, tenant, cursor string) (api.ProductEmailRecoveryPage, error) {
	var page api.ProductEmailRecoveryPage
	scope, err := s.ownerEmailScope(ctx, acct, app, tenant)
	if err != nil {
		return page, err
	}
	repo, err := s.ownerEmailRepository()
	if err != nil {
		return page, err
	}
	return repo.Recoveries(ctx, scope, "", cursor, time.Now().UTC())
}
func emailRecoveryRespond(w http.ResponseWriter, value any, err error) {
	if errors.Is(err, productemail.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_email_recovery_conflict", "Email recovery unavailable", "Recovery requires a hard-bounce pause, a different verified address, explicit consent and the current preference version and recipient generation. Refresh status before reviewing a new request; repeat an uncertain request unchanged."))
		return
	}
	emailRespond(w, value, err)
}
