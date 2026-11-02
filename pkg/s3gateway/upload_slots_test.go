// adr: 20261023223000

package s3gateway

import (
	"context"
	"testing"
	"time"
)

func TestPutSlotsKeepRoomForOtherAccounts(t *testing.T) {
	h := &Handler{putSlots: make(chan struct{}, 4)}
	for i, step := range []struct {
		account string
		acquire bool
		want    bool
	}{
		{"busy", true, true},
		{"busy", true, true},
		{"busy", true, false}, // half the slots is the account's share
		{"quiet", true, true},
		{"other", true, true},
		{"third", true, false}, // every slot is taken
		{"busy", false, false},
		{"third", true, true},
	} {
		if !step.acquire {
			h.releasePutSlot(step.account)
			continue
		}
		if _, got := h.tryPutSlot(step.account); got != step.want {
			t.Fatalf("step %d: acquire(%s) = %v, want %v", i, step.account, got, step.want)
		}
	}
	if len(h.putSlots) != 4 || h.accountSlots["busy"] != 1 {
		t.Fatalf("slots = %d, busy holds %d", len(h.putSlots), h.accountSlots["busy"])
	}
	one := &Handler{putSlots: make(chan struct{}, 1)}
	if !one.acquirePutSlot(context.Background(), "only") {
		t.Fatal("a one-slot gateway refused its first upload")
	}
}

// An SDK sends more parts at once than the account's share. The extra parts
// wait for a turn instead of answering SlowDown (amendment 2026-11-01).
func TestPutSlotWaitsForTheAccountsShare(t *testing.T) {
	h := &Handler{putSlots: make(chan struct{}, 4)}
	for range 2 {
		if !h.acquirePutSlot(context.Background(), "busy") {
			t.Fatal("the account's share was refused")
		}
	}
	got := make(chan bool, 1)
	go func() { got <- h.acquirePutSlotWithin(context.Background(), "busy", time.Minute) }()
	select {
	case <-got:
		t.Fatal("a part past the share did not wait")
	case <-time.After(20 * time.Millisecond):
	}
	// Another account's upload neither waits nor wakes the waiter with a slot.
	if _, ok := h.tryPutSlot("quiet"); !ok {
		t.Fatal("another account waited behind the busy one")
	}
	h.releasePutSlot("quiet")
	h.releasePutSlot("busy")
	if !<-got {
		t.Fatal("a waiting part did not take the freed slot")
	}
	if h.accountSlots["busy"] != 2 || len(h.putSlots) != 2 {
		t.Fatalf("busy holds %d of %d taken slots", h.accountSlots["busy"], len(h.putSlots))
	}
}

func TestPutSlotWaitIsBounded(t *testing.T) {
	h := &Handler{putSlots: make(chan struct{}, 1)}
	if !h.acquirePutSlot(context.Background(), "busy") {
		t.Fatal("first upload refused")
	}
	if h.acquirePutSlotWithin(context.Background(), "busy", 10*time.Millisecond) {
		t.Fatal("a part took a slot nobody released")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if h.acquirePutSlotWithin(ctx, "other", time.Minute) {
		t.Fatal("a cancelled request took a slot")
	}
	if len(h.putSlots) != 1 || h.accountSlots["other"] != 0 {
		t.Fatalf("slots = %d, other holds %d", len(h.putSlots), h.accountSlots["other"])
	}
}
