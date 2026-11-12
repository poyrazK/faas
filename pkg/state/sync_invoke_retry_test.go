package state_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 32 H32-65: the sync invoke endpoint marks its row so a failure the
// app returned is not retried after the caller has it. The mark keeps the
// policy's budget and backoff, and a replay of the row is async again.
func TestSyncInvokeRetryPolicyMark(t *testing.T) {
	marked := state.MarkSyncInvokeRetryPolicy(json.RawMessage(`{"max_attempts":25,"base_seconds":2}`))
	inv := state.Invocation{Source: state.InvocationAsyncInvoke, RetryPolicyJSON: marked}
	if !state.IsSyncInvoke(inv) || state.RetriesApplicationFailures(inv) {
		t.Fatalf("marked sync invoke: sync=%v retries=%v, want sync and no app retry", state.IsSyncInvoke(inv), state.RetriesApplicationFailures(inv))
	}
	if p := inv.RetryPolicy(); p.MaxAttempts != 25 || p.BaseSeconds != 2 {
		t.Fatalf("marked policy decodes as %+v, want the original budget and backoff", p)
	}
	for _, tc := range []struct {
		name string
		inv  state.Invocation
	}{
		{"plain async invoke", state.Invocation{Source: state.InvocationAsyncInvoke, RetryPolicyJSON: json.RawMessage(`{"max_attempts":25}`)}},
		{"async invoke without a policy", state.Invocation{Source: state.InvocationAsyncInvoke}},
		{"replay of a sync invoke", state.Invocation{Source: state.InvocationReplay, RetryPolicyJSON: marked}},
	} {
		if state.IsSyncInvoke(tc.inv) || !state.RetriesApplicationFailures(tc.inv) {
			t.Errorf("%s: sync=%v retries=%v, want async retry rule", tc.name, state.IsSyncInvoke(tc.inv), state.RetriesApplicationFailures(tc.inv))
		}
	}
	if got := string(state.MarkSyncInvokeRetryPolicy(nil)); got != `{"sync_invoke":true}` {
		t.Fatalf("mark of an empty policy = %s", got)
	}
}
