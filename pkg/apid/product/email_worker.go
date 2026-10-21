package product

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

// RunEmail owns the bounded poll loop. Its cursor advances over subscribers;
// each subscriber also retains its own inbox scan cursor across restarts.
func (s *Service) RunEmail(ctx context.Context, report func(error)) {
	ticker := time.NewTicker(api.ProductControlEmailPoll)
	defer ticker.Stop()
	cursor := ""
	for {
		cycle, cancel := context.WithTimeout(ctx, api.ProductControlEmailCycleTimeout)
		next, err := s.EmailStep(cycle, cursor)
		cancel()
		cursor = next
		if err != nil && report != nil && ctx.Err() == nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) EmailStep(ctx context.Context, after string) (string, error) {
	if !s.Enabled || s.EmailTransport == nil || s.EmailOpen == nil || (s.EmailReady != nil && !s.EmailReady()) {
		return after, nil
	}
	repositories, ok := s.Host.Store().(state.ProductEmailStore)
	if !ok {
		return after, state.ErrProductBindingUnavailable
	}
	repository := repositories.ProductEmailRepository()
	// Drain before scanning, so a slow Auth realm cannot starve queued jobs.
	for range api.ProductControlEmailBatchSize {
		jobs, err := repository.Claim(ctx, time.Now().UTC())
		if err != nil {
			return after, err
		}
		if len(jobs) == 0 {
			break
		}
		for _, job := range jobs {
			if err = s.deliverEmail(ctx, repository, job); err != nil && !errors.Is(err, productemail.ErrConflict) {
				return after, err
			}
		}
	}
	rows, err := repository.List(ctx, after, time.Now().UTC())
	if err != nil {
		return after, err
	}
	var scanErr error
	for _, sub := range rows {
		if ctx.Err() != nil {
			return after, ctx.Err()
		}
		if err = s.scanEmail(ctx, repository, sub); err != nil && !errors.Is(err, productemail.ErrConflict) && !errors.Is(err, productemail.ErrLimit) && !emailAccessDenied(err) {
			scanErr = err
		}
		after = sub.ID()
	}
	if len(rows) < api.ProductControlEmailBatchSize {
		after = ""
	}
	return after, scanErr
}

func (s *Service) emailRealm(ctx context.Context, sub productemail.Subscription) (customerRealm, error) {
	r := (&http.Request{}).WithContext(ctx)
	r.SetPathValue("slug", sub.AppSlug)
	realm, err := s.customerRealm(r)
	if err != nil {
		return realm, err
	}
	if _, err := customerRecoveryURL(realm); err != nil {
		return realm, err
	}
	if realm.account.ID != sub.AccountID || realm.app.ID != sub.AppID || realm.authApp.ID != sub.AuthAppID || realm.authApp.Slug != sub.AuthSlug {
		return realm, state.ErrProductAccess
	}
	return realm, nil
}
func (s *Service) emailResolve(ctx context.Context, sub productemail.Subscription) (emailRecipient, error) {
	var recipient emailRecipient
	realm, err := s.emailRealm(ctx, sub)
	if err != nil {
		return recipient, err
	}
	token, err := s.EmailOpen(sub.ID(), sub.Grant)
	if err != nil {
		return recipient, state.ErrProductAccess
	}
	err = s.authCallAuthorization(ctx, realm, http.MethodGet, "/authz/v1/orgs/"+sub.OrganizationID+"/email-recipient/"+sub.AppID, "Product-Recipient "+token, nil, &recipient)
	if err != nil {
		return recipient, err
	}
	address, err := netmail.ParseAddress(recipient.Email)
	if !emailSelfValid(recipient, sub.OrganizationID, recipient.Role) || recipient.ActorID != sub.ActorID || recipient.ProductID != sub.AppID || !recipient.ExpiresAt.After(time.Now()) || err != nil || address.Address != recipient.Email || len(recipient.Email) > api.ProductControlTeamEmailMaxBytes || strings.ContainsAny(recipient.Email, "\r\n") {
		return recipient, state.ErrProductAccess
	}
	return recipient, nil
}
func emailScope(sub productemail.Subscription, role string) state.ProductNotificationScope {
	return state.ProductNotificationScope{ProductPlanScope: state.ProductPlanScope{AccountID: sub.AccountID, AppID: sub.AppID, TenantID: sub.TenantID, AuthAppID: sub.AuthAppID, AuthApp: sub.AuthSlug, OrganizationID: sub.OrganizationID, Role: role}}
}
func (s *Service) scanEmail(ctx context.Context, repository productemail.Store, sub productemail.Subscription) error {
	recipient, err := s.emailResolve(ctx, sub)
	if err != nil {
		return err
	}
	store, ok := s.Host.Store().(state.ProductNotificationStore)
	if !ok {
		return state.ErrProductBindingUnavailable
	}
	page, err := store.ListProductNotifications(ctx, emailScope(sub, recipient.Role), "all", sub.Cursor)
	if err != nil {
		return err
	}
	if err = repository.Enqueue(ctx, sub, page.Items, time.Now().UTC()); err != nil {
		return err
	}
	return repository.Progress(ctx, sub.ID(), sub.Version, page.NextCursor)
}

func emailAccessDenied(err error) bool {
	var failure customerAuthFailure
	return errors.Is(err, state.ErrProductAccess) || errors.Is(err, state.ErrNotFound) || errors.As(err, &failure) && (failure.status == http.StatusUnauthorized || failure.status == http.StatusForbidden)
}
func (s *Service) deliverEmail(ctx context.Context, repository productemail.Store, d productemail.Delivery) error {
	sub, err := repository.Ready(ctx, d, time.Now().UTC())
	if err != nil {
		if errors.Is(err, productemail.ErrSuppressed) {
			return repository.Finish(ctx, d, "cancelled", "recipient_suppressed", time.Now().UTC(), time.Now().UTC())
		}
		if errors.Is(err, productemail.ErrConflict) {
			return repository.Finish(ctx, d, "cancelled", "preferences_changed", time.Now().UTC(), time.Now().UTC())
		}
		return err
	}
	recipient, err := s.emailResolve(ctx, sub)
	if err != nil {
		if emailAccessDenied(err) {
			return repository.Finish(ctx, d, "cancelled", "access_changed", time.Now().UTC(), time.Now().UTC())
		}
		return emailRetry(ctx, repository, d, "authorization_unavailable", 0)
	}
	store, ok := s.Host.Store().(state.ProductNotificationStore)
	if !ok {
		return emailRetry(ctx, repository, d, "authorization_unavailable", 0)
	}
	n, err := store.GetProductNotification(ctx, emailScope(sub, recipient.Role), d.NotificationID)
	if err != nil {
		if emailAccessDenied(err) {
			return repository.Finish(ctx, d, "cancelled", "access_changed", time.Now().UTC(), time.Now().UTC())
		}
		return emailRetry(ctx, repository, d, "authorization_unavailable", 0)
	}
	if n.Revision != d.Notification.Revision {
		return repository.Finish(ctx, d, "cancelled", "source_changed", time.Now().UTC(), time.Now().UTC())
	}
	unsubscribe, err := s.emailUnsubscribeURL(ctx, repository, d)
	if err != nil {
		if errors.Is(err, productemail.ErrConflict) {
			return repository.Finish(ctx, d, "cancelled", "preferences_changed", time.Now().UTC(), time.Now().UTC())
		}
		return emailRetry(ctx, repository, d, "transport_unavailable", 0)
	}
	msg, err := s.emailMessage(sub, d, recipient.Email, unsubscribe)
	if err != nil {
		return emailRetry(ctx, repository, d, "transport_unavailable", 0)
	}
	if s.EmailTransport.Mode == "resend" && s.EmailSeal != nil {
		d.RecipientSealed, err = s.EmailSeal(productemail.RecipientEvidenceID(sub.Key), productemail.RecipientEvidence(recipient.Email))
		if err != nil {
			return emailRetry(ctx, repository, d, "transport_unavailable", 0)
		}
	}
	// Last durable preference/lease check directly precedes the external send.
	if _, err = repository.Ready(ctx, d, time.Now().UTC()); err != nil {
		if errors.Is(err, productemail.ErrSuppressed) {
			return repository.Finish(ctx, d, "cancelled", "recipient_suppressed", time.Now().UTC(), time.Now().UTC())
		}
		if errors.Is(err, productemail.ErrConflict) {
			return repository.Finish(ctx, d, "cancelled", "preferences_changed", time.Now().UTC(), time.Now().UTC())
		}
		return err
	}
	if !s.Enabled {
		return emailRetry(ctx, repository, d, "transport_unavailable", 0)
	}
	send, cancel := context.WithTimeout(ctx, api.ProductControlEmailSendTimeout)
	defer cancel()
	result, err := s.EmailTransport.SendWithReceipt(send, d.ID, msg)
	if err == nil {
		if result.Status == "provider_accepted" {
			return repository.Accept(ctx, d, result.ProviderID, time.Now().UTC())
		}
		return repository.Finish(ctx, d, result.Status, "", time.Now().UTC(), time.Now().UTC())
	}
	var transient *mail.TransientError
	if errors.As(err, &transient) {
		return emailRetry(ctx, repository, d, "transport_unavailable", transient.RetryAfter)
	}
	if s.EmailTransport.Mode == "local" {
		return emailRetry(ctx, repository, d, "transport_unavailable", 0)
	}
	return repository.Finish(ctx, d, "failed", "provider_rejected", time.Now().UTC(), time.Now().UTC())
}
func emailRetry(ctx context.Context, repository productemail.Store, d productemail.Delivery, reason string, retryAfter time.Duration) error {
	now := time.Now().UTC()
	delay := api.ProductControlEmailRetryBase * time.Duration(1<<max(d.Attempts-1, 0))
	if retryAfter > delay {
		delay = retryAfter
	}
	next := now.Add(delay)
	if d.Attempts >= api.ProductControlEmailMaxAttempts || !next.Before(d.Deadline) {
		return repository.Finish(ctx, d, "failed", "retry_exhausted", now, now)
	}
	return repository.Finish(ctx, d, "queued", reason, next, now)
}
func (s *Service) emailUnsubscribeURL(ctx context.Context, repository productemail.Store, d productemail.Delivery) (string, error) {
	if s.EmailSeal == nil || s.EmailOpen == nil {
		return "", state.ErrProductBindingUnavailable
	}
	token, err := productemail.NewUnsubscribeToken()
	if err != nil {
		return "", err
	}
	hash, err := productemail.UnsubscribeHash(token)
	if err != nil {
		return "", err
	}
	id := productemail.UnsubscribeSealID(d.ID)
	sealed, err := s.EmailSeal(id, token)
	if err != nil {
		return "", err
	}
	sealed, err = repository.PrepareUnsubscribe(ctx, d, hash, sealed, time.Now().UTC())
	if err != nil {
		return "", err
	}
	token, err = s.EmailOpen(id, sealed)
	if err != nil {
		return "", err
	}
	if _, err = productemail.UnsubscribeHash(token); err != nil {
		return "", err
	}
	return s.EmailOrigin + "/v1/product-control/email/unsubscribe?token=" + token, nil
}
func (s *Service) emailMessage(sub productemail.Subscription, d productemail.Delivery, address, unsubscribe string) (mail.Message, error) {
	origin, err := url.Parse(s.EmailOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return mail.Message{}, state.ErrProductBindingUnavailable
	}
	labels := map[string]string{"work_succeeded": "A run completed.", "work_failed": "A run failed.", "work_cancelled": "A run was cancelled.", "work_attention": "A run needs your product team's attention.", "help_replied": "Your product team updated a help request.", "help_resolved": "Your product team resolved a help request."}
	label, ok := labels[d.Kind]
	if !ok {
		return mail.Message{}, state.ErrInvalidArgument
	}
	link := s.EmailOrigin + "/v1/product-control/portal?app=" + url.QueryEscape(sub.AppSlug)
	humanUnsubscribe := strings.Replace(unsubscribe, "?token=", "#token=", 1)
	text := fmt.Sprintf("%s\n\nProduct: %s\nReference: %s\n\nSign in to view your workspace updates:\n%s\n\nManage your email preferences in the customer portal, or unsubscribe without signing in:\n%s\n", label, sub.AppSlug, d.Notification.ReferenceID, link, humanUnsubscribe)
	msg := mail.Message{To: []string{address}, Subject: "Your product has an update", TextBody: text, HTMLBody: "<p>" + html.EscapeString(label) + "</p><p>Product: " + html.EscapeString(sub.AppSlug) + "<br>Reference: " + html.EscapeString(d.Notification.ReferenceID) + "</p><p><a href=\"" + html.EscapeString(link) + "\">Open your customer portal</a></p><p>Manage your email preferences in the customer portal, or <a href=\"" + html.EscapeString(humanUnsubscribe) + "\">unsubscribe without signing in</a>.</p>", MessageID: "product-email/" + d.ID}
	return mail.ApplyMarketingHeaders(msg, unsubscribe), nil
}
