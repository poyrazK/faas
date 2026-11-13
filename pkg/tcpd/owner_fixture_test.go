package tcpd

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// Routing-only fixtures have an active personal owner. Eligibility and company
// policy tests use real stores so their updates remain authoritative.
type activeOwnerFixture struct{}

func (activeOwnerFixture) AccountByID(_ context.Context, id string) (state.Account, error) {
	return state.Account{ID: id, Status: state.AccountActive}, nil
}

func (activeOwnerFixture) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}
