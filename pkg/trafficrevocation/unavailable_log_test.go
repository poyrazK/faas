package trafficrevocation

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// TestRegistryLogsAdmissionReadFailure pins Hunt 32 H32-50: requests were
// refused as traffic_revocation_unavailable for minutes with no log naming
// the cause. A failed admission read now logs once per second, not per
// request.
func TestRegistryLogsAdmissionReadFailure(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	scope := Scope{Kind: "app", ID: "a1"}
	store := &testStore{states: map[Scope]State{scope: {Revision: 1}}}
	store.fail(errors.New("store disconnected"))
	r := New(store)
	defer r.Close()
	for range 3 {
		if _, err := r.Admit(context.Background(), []Scope{scope}, func(error) {}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Admit err = %v, want ErrUnavailable", err)
		}
	}
	out := logs.String()
	if got := strings.Count(out, "admission read failed"); got != 1 {
		t.Fatalf("logged %d admission read failures, want 1 per second:\n%s", got, out)
	}
	if !strings.Contains(out, "store disconnected") {
		t.Fatalf("log does not name the cause:\n%s", out)
	}
}
