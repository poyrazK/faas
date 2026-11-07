package middleware

import "github.com/onebox-faas/faas/pkg/state"

// AccountRequiresMFA describes the customer's configured second-factor policy.
// Provider/operator operations independently require their strict proof.
func AccountRequiresMFA(acct state.Account) bool {
	return acct.MFARequired || acct.MFAEnrolled()
}

func (m *Middleware) accountRequiresMFA(acct state.Account) bool {
	return AccountRequiresMFA(acct) || m.MandatoryMFA != nil && m.MandatoryMFA(acct)
}
