package state_test

// adr: 20261103164725

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type overageCapsTestStore interface {
	state.OverageCapsReader
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	UpdateAccountOverageCapCents(ctx context.Context, accountID string, cents *int64) error
}

func TestMemAccountOverageCaps(t *testing.T) {
	testAccountOverageCaps(t, state.NewMemStore())
}

// testAccountOverageCaps checks the bulk read returns exactly the accounts
// with a cap, a zero cap included.
func testAccountOverageCaps(t *testing.T, s overageCapsTestStore) {
	t.Helper()
	ctx := context.Background()
	create := func(cap *int64) string {
		t.Helper()
		acct, err := s.CreateAccount(ctx, "caps-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		if cap != nil {
			if err := s.UpdateAccountOverageCapCents(ctx, acct.ID, cap); err != nil {
				t.Fatalf("UpdateAccountOverageCapCents: %v", err)
			}
		}
		return acct.ID
	}
	hundred, zero := int64(100), int64(0)
	capped := create(&hundred)
	zeroCapped := create(&zero)
	uncapped := create(nil)

	caps, err := s.AccountOverageCaps(ctx, []string{capped, zeroCapped, uncapped, uuid.NewString()})
	if err != nil {
		t.Fatalf("AccountOverageCaps: %v", err)
	}
	if len(caps) != 2 || caps[capped] != 100 {
		t.Fatalf("caps = %v, want %s=100 and %s=0 only", caps, capped, zeroCapped)
	}
	if c, ok := caps[zeroCapped]; !ok || c != 0 {
		t.Fatalf("zero cap = %d, %v; want present and 0", c, ok)
	}
}
