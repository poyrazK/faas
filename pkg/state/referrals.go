package state

import "github.com/onebox-faas/faas/pkg/referrals"

// ReferralStore is an optional acquisition surface, outside the broad runtime
// Store interface. It has no VM, operator, or payment-provider authority.
func (s *PgStore) ReferralStore() referrals.Store { return referrals.NewPostgresStore(s.pool.Pool) }
