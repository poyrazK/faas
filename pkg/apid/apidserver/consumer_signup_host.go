package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/consumersignup"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerSignupHost adapts *server to consumersignup.Host (ADR-982,
// ADR-20261021143000).
type consumerSignupHost struct{ s *server }

var _ consumersignup.Host = consumerSignupHost{}

func (h consumerSignupHost) Store() state.Store { return h.s.store }

func (h consumerSignupHost) Released(accountID string) bool { return h.s.apiMonetizationFor(accountID) }

func (h consumerSignupHost) AppRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool) {
	return h.s.consumerApp(w, r, acct)
}

func (h consumerSignupHost) ConsumerFeatureAllowed(w http.ResponseWriter, acct state.Account) bool {
	return h.s.consumerFeatureAllowed(w, acct)
}

func (h consumerSignupHost) KeyScopesProblem(accountID string, scopes []string) *api.Problem {
	return h.s.consumerKeyScopesProblem(accountID, scopes)
}

func (h consumerSignupHost) StripeKey(ctx context.Context, accountID string) (string, bool, *api.Problem) {
	return h.s.statementInvoices.StripeKey(ctx, accountID)
}

func (h consumerSignupHost) IssueConsumerKey(ctx context.Context, acct state.Account, consumer state.APIConsumer, name string,
	scopes []string) (state.ConsumerKey, string, *api.Problem) {
	return h.s.issueConsumerKey(ctx, acct, consumer, name, scopes, nil)
}

func (h consumerSignupHost) Audit(ctx context.Context, event, accountID string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, &accountID, fields)
}

func (h consumerSignupHost) PublicOrigin() string { return "https://" + h.s.domain }

func (h consumerSignupHost) ClientIP(r *http.Request) string { return middleware.ClientIP(r) }
