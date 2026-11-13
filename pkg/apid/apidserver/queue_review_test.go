// adr: 20261112200000 — queue review fixes (2026-11-12).
package apidserver

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A pull consumer's nacks are budgeted by the message's policy, which the
// send stamped from the app: the binding's retry_policy reached only push.
// The send now stamps the binding's policy when it names none of its own.
func TestQueueRetryPolicyPrecedence(t *testing.T) {
	app := state.App{RetryPolicyJSON: json.RawMessage(`{"max_attempts":4}`)}
	binding := &state.QueueBinding{RetryPolicyJSON: json.RawMessage(`{"max_attempts":2,"base_seconds":5}`)}
	attempts := func(raw json.RawMessage) int {
		t.Helper()
		var p api.RetryPolicyDTO
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		return p.MaxAttempts
	}
	if got := attempts(effectiveQueueRetryPolicy(app, binding, nil, 10)); got != 2 {
		t.Fatalf("binding policy: max_attempts %d, want 2", got)
	}
	if got := attempts(effectiveQueueRetryPolicy(app, binding, &api.RetryPolicyDTO{MaxAttempts: 7}, 10)); got != 7 {
		t.Fatalf("send override: max_attempts %d, want 7", got)
	}
	if got := attempts(effectiveQueueRetryPolicy(app, &state.QueueBinding{RetryPolicyJSON: json.RawMessage(`{}`)}, nil, 10)); got != 4 {
		t.Fatalf("empty binding policy: max_attempts %d, want the app's 4", got)
	}
	if got := attempts(effectiveQueueRetryPolicy(app, nil, nil, 10)); got != 4 {
		t.Fatalf("no binding: max_attempts %d, want the app's 4", got)
	}
	if got := attempts(effectiveQueueRetryPolicy(app, &state.QueueBinding{RetryPolicyJSON: json.RawMessage(`{"max_attempts":25}`)}, nil, 3)); got != 3 {
		t.Fatalf("binding policy over the plan: max_attempts %d, want the plan's 3", got)
	}
}
