package state

import "testing"

// Billing review R8-2: the checkpoint arithmetic SubtractInternalEgress
// shares between PgStore and MemStore.
func TestInternalEgressStep(t *testing.T) {
	cases := []struct {
		name                                 string
		prev                                 int64
		prevValid                            bool
		pending, fresh, cumulative, rowNetTx int64
		wantSubtract, wantPending            int64
	}{
		{name: "first reading counts in full", cumulative: 300, rowNetTx: 1000, wantSubtract: 300},
		{name: "delta since checkpoint", prev: 300, prevValid: true, cumulative: 500, rowNetTx: 1000, wantSubtract: 200},
		{name: "reset counts in full", prev: 300, prevValid: true, cumulative: 100, rowNetTx: 1000, wantSubtract: 100},
		{name: "row too small carries", prevValid: true, cumulative: 500, rowNetTx: 200, wantSubtract: 200, wantPending: 300},
		{name: "carry is added", prev: 500, prevValid: true, pending: 300, cumulative: 600, rowNetTx: 1000, wantSubtract: 400},
		{name: "carry beyond one reading is dropped", prev: 500, prevValid: true, pending: 300, cumulative: 600, wantPending: 100},
		{name: "never below zero", cumulative: 50, rowNetTx: -5, wantPending: 50},
		{name: "service replies add to the reading", prev: 100, prevValid: true, fresh: 400, cumulative: 200, rowNetTx: 1000, wantSubtract: 500},
		{name: "service replies carry once", prev: 100, prevValid: true, fresh: 400, cumulative: 200, rowNetTx: 300, wantSubtract: 300, wantPending: 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subtract, pending := internalEgressStep(tc.prev, tc.prevValid, tc.pending, tc.fresh, tc.cumulative, tc.rowNetTx)
			if subtract != tc.wantSubtract || pending != tc.wantPending {
				t.Fatalf("internalEgressStep = (%d, %d), want (%d, %d)", subtract, pending, tc.wantSubtract, tc.wantPending)
			}
		})
	}
}
