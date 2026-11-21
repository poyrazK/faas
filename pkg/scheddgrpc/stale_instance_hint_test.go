package scheddgrpc_test

import (
	"context"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

// staleHintEngine records the stale instance hints AdmitInstance hands the
// engine before it admits.
type staleHintEngine struct {
	fakeEngine
	mu     sync.Mutex
	hints  [][2]string
	admits int
}

func (e *staleHintEngine) ReconcileStaleInstanceHint(_ context.Context, appID, instanceID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hints = append(e.hints, [2]string{appID, instanceID})
	return true
}

func (e *staleHintEngine) AdmitInstance(context.Context, string, string, string, string) (sched.WakeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.admits++
	return sched.WakeResult{InstanceID: "replacement", NodeID: "node", WakeID: "wake"}, nil
}

func TestAdmitInstanceCarriesStaleInstanceHint(t *testing.T) {
	cases := []struct {
		name      string
		ctx       func(context.Context) context.Context
		wantHints int
	}{
		{name: "no hint", ctx: func(ctx context.Context) context.Context { return ctx }},
		{name: "hint", ctx: func(ctx context.Context) context.Context { return sched.WithStaleInstanceHint(ctx, "ghost") }, wantHints: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := &staleHintEngine{}
			client := newClient(t, engine)
			id, _, _, _, _, _, _, _, err := client.AdmitInstanceWithIdentity(tc.ctx(t.Context()), "app", "", "production", "stale_target_recovery")
			if err != nil || id != "replacement" {
				t.Fatalf("admit: id=%q err=%v", id, err)
			}
			engine.mu.Lock()
			defer engine.mu.Unlock()
			if len(engine.hints) != tc.wantHints || engine.admits != 1 {
				t.Fatalf("hints=%v admits=%d", engine.hints, engine.admits)
			}
			if tc.wantHints == 1 && engine.hints[0] != [2]string{"app", "ghost"} {
				t.Fatalf("hint=%v", engine.hints[0])
			}
		})
	}
}
