package product

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

type emailRecipient struct {
	ActorID        string    `json:"actor_id"`
	OrganizationID string    `json:"organization_id"`
	ProductID      string    `json:"product_id"`
	Role           string    `json:"role"`
	Email          string    `json:"email"`
	Token          string    `json:"token"`
	ExpiresAt      time.Time `json:"expires_at"`
	Protocol       int       `json:"protocol"`
}
type emailCustomer struct {
	store productemail.Store
	realm customerRealm
	scope state.ProductNotificationScope
	key   productemail.Key
	token string
}

func canonicalEmailID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
func emailSelfValid(v emailRecipient, org, role string) bool {
	return v.Protocol == 1 && canonicalEmailID(v.ActorID) && v.OrganizationID == org && productcontrol.ValidAccessRole(v.Role) && v.Role == role
}
func (s *Service) emailCustomer(r *http.Request) (emailCustomer, error) {
	var c emailCustomer
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok {
		return c, state.ErrProductAccess
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		return c, err
	}
	if _, err := customerRecoveryURL(realm); err != nil {
		return c, err
	}
	if realm.authApp.ID != id.AuthAppID || realm.authApp.Slug != id.AuthApp {
		return c, state.ErrProductAccess
	}
	st, ok := s.Host.Store().(state.ProductEmailStore)
	if !ok {
		return c, state.ErrProductBindingUnavailable
	}
	cookie, err := r.Cookie(realm.cookieName("access"))
	if err != nil {
		return c, customerAuthFailure{http.StatusUnauthorized}
	}
	var self emailRecipient
	if err = s.authCall(r.Context(), realm, http.MethodGet, "/authz/v1/orgs/"+id.OrganizationID+"/email-recipient", cookie.Value, nil, &self); err != nil {
		return c, err
	}
	if !emailSelfValid(self, id.OrganizationID, id.Access.Role) {
		return c, state.ErrProductAccess
	}
	c = emailCustomer{store: st.ProductEmailRepository(), realm: realm, token: cookie.Value, key: productemail.Key{AccountID: realm.account.ID, AppID: realm.app.ID, TenantID: id.TenantID, AuthAppID: id.AuthAppID, OrganizationID: id.OrganizationID, ActorID: self.ActorID}, scope: state.ProductNotificationScope{ProductPlanScope: state.ProductPlanScope{AccountID: realm.account.ID, AppID: realm.app.ID, TenantID: id.TenantID, AuthAppID: id.AuthAppID, AuthApp: id.AuthApp, OrganizationID: id.OrganizationID, Role: id.Access.Role}}}
	return c, nil
}
func (s *Service) emailPreferences(v productemail.Subscription) api.ProductEmailPreferences {
	out := api.ProductEmailPreferences{DeliveryPageSize: api.ProductControlEmailBatchSize, WorkOutcomes: v.Work, HelpReplies: v.Help, Version: v.Version, Mode: "disabled", Available: s.EmailTransport != nil && s.EmailSeal != nil && s.EmailOpen != nil && (s.EmailReady == nil || s.EmailReady())}
	out.PauseReason = v.PauseReason
	out.Generation = max(int64(1), v.Generation)
	if v.PauseReason != "" {
		at := v.PausedAt
		out.PausedAt = &at
		out.RecoveryBlockedReason = productemail.RecoveryBlocked(v)
		if out.RecoveryBlockedReason == "verified_replacement_required" && s.EmailOpen != nil {
			if _, err := s.EmailOpen(productemail.RecipientEvidenceID(v.Key), v.PauseRecipient); err != nil {
				out.RecoveryBlockedReason = "recipient_evidence_unavailable"
			}
		}
		if !out.Available && v.PauseReason == "hard_bounce" {
			out.RecoveryBlockedReason = "setup_unavailable"
		}
	}
	if s.EmailTransport != nil {
		out.Mode = s.EmailTransport.Mode
	}
	if !v.ExpiresAt.IsZero() {
		at := v.ExpiresAt
		out.ExpiresAt = &at
	}
	return out
}
func (s *Service) CustomerEmailPreferences(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if _, err := ownerQuery(r); err != nil {
		emailRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	c, err := s.emailCustomer(r)
	if err != nil {
		customerError(w, err)
		return
	}
	v, err := c.store.Get(r.Context(), c.key)
	emailRespond(w, s.emailPreferences(v), err)
}
func (s *Service) CustomerEmailUpdate(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if _, err := ownerQuery(r); err != nil {
		emailRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	var req api.ProductEmailUpdate
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlNotificationBodyMaxBytes) {
		return
	}
	if req.WorkOutcomes == nil || req.HelpReplies == nil || req.ExpectedVersion == nil || *req.ExpectedVersion < 0 {
		emailRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	c, err := s.emailCustomer(r)
	if err != nil {
		customerError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
	defer cancel()
	var next productemail.Subscription
	err = c.store.WithConsent(ctx, c.key, func() error {
		old, e := c.store.Get(ctx, c.key)
		if e != nil {
			return e
		}
		if old.Version != *req.ExpectedVersion {
			return productemail.ErrConflict
		}
		next = old
		next.Key = c.key
		next.AppSlug, next.AuthSlug = c.realm.app.Slug, c.realm.authApp.Slug
		next.Work, next.Help = *req.WorkOutcomes, *req.HelpReplies
		if next.Work || next.Help {
			next, e = s.emailConsent(ctx, c, next)
			if e != nil {
				return e
			}
		}
		next, e = c.store.Put(ctx, next, *req.ExpectedVersion, time.Now().UTC().Truncate(time.Microsecond))
		return e
	})
	emailRespond(w, s.emailPreferences(next), err)
}

func (s *Service) emailConsent(ctx context.Context, c emailCustomer, next productemail.Subscription) (productemail.Subscription, error) {
	if !s.emailPreferences(next).Available {
		return next, state.ErrProductBindingUnavailable
	}
	var grant emailRecipient
	err := s.authCall(ctx, c.realm, http.MethodPost, "/authz/v1/orgs/"+c.key.OrganizationID+"/email-recipient", c.token, map[string]string{"product_id": c.key.AppID}, &grant)
	if err != nil {
		return next, err
	}
	if !emailSelfValid(grant, c.key.OrganizationID, c.scope.Role) || grant.ActorID != c.key.ActorID || grant.ProductID != c.key.AppID || !grant.ExpiresAt.After(time.Now()) || grant.ExpiresAt.After(time.Now().Add(time.Duration(api.ProductControlEmailGrantSeconds)*time.Second)) {
		return next, state.ErrProductAccess
	}
	next.Grant, err = s.EmailSeal(c.key.ID(), grant.Token)
	next.ExpiresAt = grant.ExpiresAt
	return next, err
}
func (s *Service) CustomerEmailDeliveries(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if _, err := ownerQuery(r); err != nil {
		emailRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	c, err := s.emailCustomer(r)
	if err != nil {
		customerError(w, err)
		return
	}
	rows, err := c.store.Recent(r.Context(), c.key)
	if err != nil {
		emailRespond(w, nil, err)
		return
	}
	store, ok := s.Host.Store().(state.ProductNotificationStore)
	if !ok {
		emailRespond(w, nil, state.ErrProductBindingUnavailable)
		return
	}
	visible := []api.ProductEmailDelivery{}
	for _, row := range rows {
		if _, err = store.GetProductNotification(r.Context(), c.scope, row.NotificationID); err == nil {
			visible = append(visible, row)
		} else if !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrProductAccess) {
			emailRespond(w, nil, err)
			return
		}
	}
	emailRespond(w, map[string]any{"deliveries": visible}, nil)
}
func emailRespond(w http.ResponseWriter, v any, err error) {
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("set work_outcomes, help_replies and expected_version from your current email preferences"))
		return
	}
	var auth customerAuthFailure
	if errors.As(err, &auth) {
		customerError(w, err)
		return
	}
	if errors.Is(err, productemail.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_email_conflict", "Email preferences changed", "Refresh your preferences before saving again."))
		return
	}
	if errors.Is(err, productemail.ErrLimit) {
		api.WriteProblem(w, api.ErrCapacity("The notification email limit has been reached."))
		return
	}
	if err != nil {
		notificationRespond(w, nil, err)
		return
	}
	notificationRespond(w, v, nil)
}
