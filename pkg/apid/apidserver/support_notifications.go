package apidserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) registerSupportNotificationRoutes(mux *http.ServeMux, customer func(string) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/support/notifications", customer("notifications"))
	mux.HandleFunc("POST /v1/support/tickets/{ticket}/read", customer("read"))
	mux.HandleFunc("GET /v1/orgs/{org}/support/notifications", customer("notifications"))
	mux.HandleFunc("POST /v1/orgs/{org}/support/tickets/{ticket}/read", customer("read"))
	mux.HandleFunc("GET /v1/admin/support/notifications", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("notifications", true))))
	// Personal read metadata needs a current MFA-completed staff session and a
	// trusted origin; privileged conversation/configuration writes retain step-up.
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/read", s.authLimited(s.requireSessionPrincipal(s.requireSameOrigin(s.customerSupportHandler("read", true)))))
}

func (s *server) supportNotificationBootstrapEmails() []string {
	addresses := []string{}
	if s.adminAllowlist != nil {
		for address := range s.adminAllowlist.emails {
			addresses = append(addresses, address)
		}
	}
	sort.Strings(addresses)
	return addresses
}

func (s *server) supportNotificationEmailAllowed(ctx context.Context, account, plan, org string, staff bool) (bool, error) {
	if staff || s.capGate == nil {
		return true, nil
	}
	acct, err := s.store.AccountByID(ctx, account)
	if errors.Is(err, state.ErrNotFound) {
		acct, err = s.store.AccountByID(ctx, strings.ReplaceAll(account, "-", ""))
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if org != "" {
		scope, err := s.store.OrgByID(ctx, org)
		if errors.Is(err, state.ErrNotFound) {
			scope, err = s.store.OrgByID(ctx, strings.ReplaceAll(org, "-", ""))
		}
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		org = scope.ID
	}
	return s.capGate.Enabled("customer-support", platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: org}), nil
}

func (s *server) sendSupportNotification(ctx context.Context, n support.NotificationEmail) error {
	if s.mailer == nil {
		return errors.New("support notification mailer unavailable")
	}
	base, err := url.Parse(s.cliAuthURLBase)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return errors.New("support notification dashboard URL unavailable")
	}
	link := base.JoinPath(n.Path).String()
	subject, body := mail.SupportUpdateBody(n.Kind, n.TicketID, link)
	return s.mailer.Send(ctx, Message{To: []string{n.To}, Subject: subject, TextBody: body, MessageID: n.MessageID})
}
