package gateway

import "context"

// WithFinancialBudgetAdmission checks warm requests before edge answers,
// authentication, wake and proxy. The production callback reads current rows,
// independently of the route cache and its stale fallback.
func (h *Handler) WithFinancialBudgetAdmission(check func(context.Context, App) error) *Handler {
	h.financialBudgetAdmission = check
	return h
}
