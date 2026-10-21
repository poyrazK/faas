package promotion

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

type claimedLeaseFixture struct {
	healthControlStore
	current  bool
	leaseErr error
	releases int
}

func (*claimedLeaseFixture) ClaimNextProjectEnvironmentPromotionHealth(context.Context, string, bool) (*state.PromotionHealthJob, error) {
	return nil, nil
}
func (s *claimedLeaseFixture) ProjectEnvironmentPromotionHealthLeaseCurrent(context.Context, string, string, string) (bool, error) {
	return s.current, s.leaseErr
}
func (s *claimedLeaseFixture) ReleaseProjectEnvironmentPromotionHealth(context.Context, string, string) error {
	s.releases++
	return nil
}
func TestClaimedHealthStaleLeaseAndCanceledRead(t *testing.T) {
	for _, fail := range []bool{false, true} {
		store := &claimedLeaseFixture{}
		if fail {
			store.leaseErr = context.Canceled
		}
		err := ReconcileClaimedHealth(t.Context(), store, state.PromotionHealthJob{PromotionID: "old"}, HealthActions{}, "old-token")
		if fail && !errors.Is(err, context.Canceled) || !fail && err != nil || store.releases != 1 {
			t.Fatal("claim cleanup failed", err, store.releases)
		}
	}
}
