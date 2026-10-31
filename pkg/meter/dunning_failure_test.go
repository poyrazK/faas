package meter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// stuckDunning fails every dunning transition for one account.
type stuckDunning struct {
	MemStore *state.MemStore
	stuck    string
}

func (s stuckDunning) MarkDunningStep(ctx context.Context, id string, from, to state.AccountStatus, opts ...state.AccountDeletionOption) error {
	if id == s.stuck {
		return errors.New("database unavailable")
	}
	return s.MemStore.MarkDunningStep(ctx, id, from, to, opts...)
}

// A transition that fails for one account fails the dunning tick, so the
// dunning alert sees it, while the sweep still moves the other accounts on.
// adr: 20261030120000
func TestDunningRunOnceReportsAStuckTransition(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var ids []string
	for _, email := range []string{"stuck@example.com", "moving@example.com"} {
		acct, err := mem.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		if err := mem.UpdateAccountStatus(ctx, acct.ID, state.AccountPastDue); err != nil {
			t.Fatal(err)
		}
		if err := mem.SetPastDueAtForTest(acct.ID, now.Add(-DunningPastDueToSuspendedDuration-time.Hour)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, acct.ID)
	}
	d := NewDunning(DunningParams{Store: statetest.NewMem(stuckDunning{MemStore: mem, stuck: ids[0]}), Now: func() time.Time { return now }})
	err := d.RunOnce(ctx)
	if err == nil {
		t.Fatal("RunOnce hid a failed suspension")
	}
	moved, getErr := mem.AccountByID(ctx, ids[1])
	if getErr != nil {
		t.Fatal(getErr)
	}
	if moved.Status != state.AccountSuspended {
		t.Fatalf("other account status = %s, want suspended: one failure must not stop the sweep (err %v)", moved.Status, err)
	}
}
