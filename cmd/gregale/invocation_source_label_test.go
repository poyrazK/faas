package main_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Hunt 33 H33-16: a synchronous `gregale invoke` listed as async_invoke.
func TestInvocationSourceLabelNamesSyncInvokes(t *testing.T) {
	for _, tc := range []struct {
		source, policy, want string
	}{
		{"async_invoke", `{"sync_invoke":true,"max_attempts":25}`, "sync_invoke"},
		{"async_invoke", `{"max_attempts":3}`, "async_invoke"},
		{"async_invoke", ``, "async_invoke"},
		{"queue", `{"sync_invoke":true}`, "queue"},
	} {
		inv := api.Invocation{Source: tc.source}
		if tc.policy != "" {
			inv.RetryPolicyJSON = json.RawMessage(tc.policy)
		}
		if got := main.InvocationSourceLabel(inv); got != tc.want {
			t.Errorf("source=%s policy=%s: %s, want %s", tc.source, tc.policy, got, tc.want)
		}
	}
}
