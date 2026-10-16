package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// RealtimePlanLimits lets the owner dashboard reuse the managed surface's plan gate.
func (s *Service) RealtimePlanLimits(acct state.Account) (api.RealtimeLimits, bool) {
	return s.realtimePlanLimits(acct)
}
