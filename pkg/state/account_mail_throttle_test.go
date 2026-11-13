package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The shared mail counter caps a kind per window, spaces sends by the
// minimum interval, counts kinds apart and starts over in a new window.
func TestTakeAccountMailSlot(t *testing.T) {
	ctx := t.Context()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "slots@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	capped := state.MailSlotLimit{Window: time.Hour, MaxPerWindow: 2}
	take := func(kind string, limit state.MailSlotLimit) bool {
		t.Helper()
		ok, err := m.TakeAccountMailSlot(ctx, acct.ID, kind, limit)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !take("k", capped) || !take("k", capped) || take("k", capped) {
		t.Fatal("want two sends then a refusal")
	}
	if !take("other", capped) {
		t.Fatal("kinds share a counter")
	}
	spaced := state.MailSlotLimit{Window: time.Hour, MaxPerWindow: 5, MinInterval: time.Minute}
	if !take("s", spaced) || take("s", spaced) {
		t.Fatal("the minimum interval did not hold")
	}
	m.AgeAccountMailSlotsForTest(time.Hour)
	if !take("k", capped) || !take("s", spaced) {
		t.Fatal("a new window did not start over")
	}
	if _, err := m.TakeAccountMailSlot(ctx, "00000000-0000-0000-0000-000000000000", "k", capped); err == nil {
		t.Fatal("unknown account was counted")
	}
}
