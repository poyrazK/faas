package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// loginFailureBudget checks the shared failed-password budget: the last
// failure of a window asks for one notice, further checks wait for the
// window, addresses count apart, and a reset lifts the pause.
func loginFailureBudget(t *testing.T, st state.LoginFailureBudgetStore) {
	t.Helper()
	ctx := t.Context()
	budget := state.LoginFailureBudget{MaxFailures: 3, Window: time.Hour}
	key := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	record := func(k string) bool {
		t.Helper()
		notify, err := st.RecordLoginFailure(ctx, k, budget)
		if err != nil {
			t.Fatal(err)
		}
		return notify
	}
	wait := func(k string) time.Duration {
		t.Helper()
		d, err := st.LoginFailureRetryAfter(ctx, k, budget)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if record(key) || record(key) {
		t.Fatal("notice before the budget ran out")
	}
	if d := wait(key); d != 0 {
		t.Fatalf("paused with budget left: %s", d)
	}
	if !record(key) {
		t.Fatal("no notice when the budget ran out")
	}
	if record(key) {
		t.Fatal("second notice within the window")
	}
	if d := wait(key); d <= 0 || d > time.Hour {
		t.Fatalf("retry after = %s, want within the hour", d)
	}
	if d := wait(other); d != 0 {
		t.Fatalf("another address is paused: %s", d)
	}
	if err := st.ResetLoginFailures(ctx, key); err != nil {
		t.Fatal(err)
	}
	if d := wait(key); d != 0 {
		t.Fatalf("still paused after reset: %s", d)
	}
	if _, err := st.RecordLoginFailure(ctx, "short", budget); err == nil {
		t.Fatal("a key that is not an email hash was counted")
	}
}

func TestMemLoginFailureBudget(t *testing.T) {
	loginFailureBudget(t, state.NewMemStore())
}
