// adr: 20261104143005 — the stats tick's recycles and ledger writes leave the tick.
package instancestats

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

func egressRow(instance string) InstanceStat {
	return InstanceStat{InstanceID: instance, AppID: "app", EgressFanoutValid: true, EgressNewDstPerMin: 10, EgressNewDstLimitPerMin: 10}
}

func TestEnforcementDispatchRunsRecyclesOffTheTick(t *testing.T) {
	var handled []string
	p := &Poller{EgressAbuseHandler: func(_ context.Context, row InstanceStat, _ sched.EgressAbuseReason, _, _ int64) error {
		handled = append(handled, row.InstanceID)
		return nil
	}}
	var keys []string
	var queued []func()
	p.WithEnforcementDispatch(func(key string, fn func()) {
		keys = append(keys, key)
		queued = append(queued, fn)
	})

	p.enforceEgressAbuse(t.Context(), []InstanceStat{egressRow("i1")})
	if len(handled) != 0 {
		t.Fatalf("the tick ran the recycle itself: %v", handled)
	}
	if !reflect.DeepEqual(keys, []string{"egress/i1"}) {
		t.Fatalf("dispatched keys = %v, want [egress/i1]", keys)
	}

	// Dropped by a full pool: not recorded, so the next tick submits again.
	p.enforceEgressAbuse(t.Context(), []InstanceStat{egressRow("i1")})
	if len(keys) != 2 {
		t.Fatalf("a dropped action was not resubmitted: keys = %v", keys)
	}

	queued[1]()
	if !reflect.DeepEqual(handled, []string{"i1"}) {
		t.Fatalf("handled = %v, want [i1]", handled)
	}
	// Taken: a later tick does not act again while the signal stays high.
	p.enforceEgressAbuse(t.Context(), []InstanceStat{egressRow("i1")})
	if len(keys) != 2 {
		t.Fatalf("an action already taken was dispatched again: keys = %v", keys)
	}
}

func TestEnforcementWithoutDispatchRunsInline(t *testing.T) {
	calls := 0
	p := &Poller{EgressAbuseHandler: func(context.Context, InstanceStat, sched.EgressAbuseReason, int64, int64) error {
		calls++
		return nil
	}}
	p.enforceEgressAbuse(t.Context(), []InstanceStat{egressRow("i1")})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// The worker applies every report in tick order, so the ledger writes exactly
// what an inline ledger writes.
func TestPauseQueueKeepsEveryReportInOrder(t *testing.T) {
	reports := [][]InstanceStat{
		{pausedRow("a", 1, 0)},
		{withLast(runningRow("a", 2), 0, 2)},
		{pausedRow("a", 4, 3)},
		{pausedRow("a", 6, 3)},
	}
	inlineStore := &fakePauseStore{}
	inline := NewPauseLedger(inlineStore, 0, nil, nil)
	for _, rows := range reports {
		inline.Observe(t.Context(), rows, rows[0].SampledAt)
	}

	queuedStore := &fakePauseStore{}
	p := &Poller{PauseLedger: NewPauseLedger(queuedStore, 0, nil, nil)}
	for _, rows := range reports {
		p.observePauses(t.Context(), rows, rows[0].SampledAt)
	}
	p.waitPauses()

	if !reflect.DeepEqual(queuedStore.writes, inlineStore.writes) {
		t.Fatalf("queued writes = %+v\ninline writes = %+v", queuedStore.writes, inlineStore.writes)
	}
}
