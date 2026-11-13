package conformance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testQueuePayloadTrim: a completed queue message loses its payload once its
// completion is past the cutoff; pending queue work and other sources keep
// theirs (ADR-20261018101500 amendment 2026-10-31).
func testQueuePayloadTrim(t *testing.T, fx *Fixture) {
	trimmer, ok := fx.Store.(state.QueuePayloadTrimStore)
	if !ok {
		t.Skip("store does not trim queue payloads")
	}
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.Store.EnsureAccountAsyncQuota(fx.Ctx, fx.Account.ID, 10); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"job":1}`)
	enqueue := func(source state.InvocationSource) state.Invocation {
		t.Helper()
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: fx.App.ID, AccountID: fx.Account.ID,
			Source: source, Payload: payload, DueAt: time.Now().UTC().Add(-time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	complete := func(inv state.Invocation) {
		t.Helper()
		if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "instance-1", 30, 10); err != nil {
			t.Fatalf("claim %s: %v", inv.Source, err)
		}
		if err := fx.Store.CompleteInvocation(fx.Ctx, inv.ID, json.RawMessage(`{"ok":true}`)); err != nil {
			t.Fatalf("complete %s: %v", inv.Source, err)
		}
	}
	done, async, pending := enqueue(state.InvocationQueue), enqueue(state.InvocationAsyncInvoke), enqueue(state.InvocationQueue)
	complete(done)
	complete(async)
	if n, err := trimmer.TrimCompletedQueuePayloads(fx.Ctx, time.Now().Add(-time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("trim before the cutoff = %d, %v; want nothing", n, err)
	}
	if n, err := trimmer.TrimCompletedQueuePayloads(fx.Ctx, time.Now().Add(time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("trim after the cutoff = %d, %v; want the completed queue message", n, err)
	}
	for _, c := range []struct {
		inv  state.Invocation
		want json.RawMessage
	}{{done, state.TrimmedQueuePayload}, {async, payload}, {pending, payload}} {
		row, err := fx.Store.InvocationByID(fx.Ctx, c.inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		if json.Unmarshal(row.Payload, &got) != nil || json.Unmarshal(c.want, &want) != nil || !jsonEqual(got, want) {
			t.Fatalf("%s %s payload = %s, want %s", row.Source, row.State, row.Payload, c.want)
		}
	}
	if n, err := trimmer.TrimCompletedQueuePayloads(fx.Ctx, time.Now().Add(time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("second trim = %d, %v; want nothing left", n, err)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
