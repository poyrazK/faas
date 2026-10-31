// adr: 20261019120000
package sched

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type dispatchGuardStub struct {
	blocked bool
	err     error
	apps    []string
}

func (s *dispatchGuardStub) ScenarioWorkerDispatchBlocked(_ context.Context, app string) (bool, error) {
	s.apps = append(s.apps, app)
	return s.blocked, s.err
}

func TestRehearsalDispatchBlocked(t *testing.T) {
	t.Parallel()
	inv := state.Invocation{ID: "inv-1", AppID: "app-1"}
	for _, tc := range []struct {
		name    string
		store   any
		want    bool
		wantLog bool
	}{
		{name: "store without guard", store: struct{}{}},
		{name: "not blocked", store: &dispatchGuardStub{}},
		{name: "blocked", store: &dispatchGuardStub{blocked: true}, want: true},
		// The claim's rehearsal triggers remain authoritative, so a failed
		// advisory read must not hold every tenant's dispatch.
		{name: "read error claims under database guards", store: &dispatchGuardStub{blocked: true, err: errors.New("pool exhausted")}, wantLog: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			got := rehearsalDispatchBlocked(context.Background(), tc.store, inv, slog.New(slog.NewTextHandler(&buf, nil)))
			if got != tc.want {
				t.Fatalf("blocked = %v, want %v", got, tc.want)
			}
			if logged := strings.Contains(buf.String(), "rehearsal dispatch pre-check failed"); logged != tc.wantLog {
				t.Fatalf("warning logged = %v, want %v; log: %s", logged, tc.wantLog, buf.String())
			}
			if stub, ok := tc.store.(*dispatchGuardStub); ok && (len(stub.apps) != 1 || stub.apps[0] != inv.AppID) {
				t.Fatalf("guard read apps = %v, want [%s]", stub.apps, inv.AppID)
			}
		})
	}
}
