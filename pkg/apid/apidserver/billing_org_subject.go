package apidserver

import "github.com/onebox-faas/faas/pkg/state"

// orgBillingSubject reports whether a verified webhook resolved to a company
// organization's billing subject rather than an account. The billing-mail
// outboxes (payment failed, account restored, subscription ended) lock,
// transition and mail account rows only, so an organization's delivery failed
// there with 503 on every attempt: its dunning never started and a revoked
// subscription kept its paid plan (Hunt 17 H17-3). Organizations stay on the
// legacy paths, whose store writes resolve organization subjects.
func orgBillingSubject(acct state.Account) bool {
	return acct.ResourceOrgID != ""
}
