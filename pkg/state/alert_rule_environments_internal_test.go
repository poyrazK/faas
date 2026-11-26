package state

import (
	"context"
	"testing"
	"time"
)

// adr: 20261104120000 — MemStore counts only the stage's own failed
// invocations, as PgStore does (TestPgAlertRuleEnvironments).
func TestMemCountFailedInvocationsInEnvironmentSince(t *testing.T) {
	m := NewMemStore()
	now := time.Now().UTC()
	m.mu.Lock()
	for i, inv := range []Invocation{
		{AccountID: "acct", AppID: "app", EnvironmentID: "", Source: InvocationCron, State: InvocationFailed},
		{AccountID: "acct", AppID: "app", EnvironmentID: "env-1", Source: InvocationCron, State: InvocationFailed},
		{AccountID: "acct", AppID: "app", EnvironmentID: "env-1", Source: InvocationQueue, State: InvocationDeadLetter},
		{AccountID: "acct", AppID: "app", EnvironmentID: "env-1", Source: InvocationCron, State: InvocationCompleted},
		{AccountID: "acct", AppID: "app", EnvironmentID: "env-2", Source: InvocationCron, State: InvocationFailed},
	} {
		inv.ID = string(rune('a' + i))
		inv.CreatedAt = now.Add(-time.Minute)
		m.invocations[inv.ID] = inv
	}
	m.mu.Unlock()
	for _, tc := range []struct {
		source InvocationSource
		want   int
	}{{"", 2}, {InvocationCron, 1}, {InvocationQueue, 1}} {
		n, err := m.CountFailedInvocationsInEnvironmentSince(context.Background(), "acct", "app", "env-1", tc.source, now.Add(-time.Hour))
		if err != nil || n != tc.want {
			t.Fatalf("source %q: %d, %v; want %d", tc.source, n, err, tc.want)
		}
	}
	if _, err := m.CountFailedInvocationsInEnvironmentSince(context.Background(), "acct", "app", "", "", now); err == nil {
		t.Fatal("production is not an environment here")
	}
}
