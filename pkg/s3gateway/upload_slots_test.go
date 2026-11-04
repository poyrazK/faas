// adr: 20261023223000

package s3gateway

import "testing"

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
		if got := h.acquirePutSlot(step.account); got != step.want {
			t.Fatalf("step %d: acquire(%s) = %v, want %v", i, step.account, got, step.want)
		}
	}
	if len(h.putSlots) != 4 || h.accountSlots["busy"] != 1 {
		t.Fatalf("slots = %d, busy holds %d", len(h.putSlots), h.accountSlots["busy"])
	}
	one := &Handler{putSlots: make(chan struct{}, 1)}
	if !one.acquirePutSlot("only") {
		t.Fatal("a one-slot gateway refused its first upload")
	}
}
