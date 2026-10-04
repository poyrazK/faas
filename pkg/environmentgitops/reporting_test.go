// adr: 532 — report attempts cannot execute or recover enforce effects.
package environmentgitops_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type recoveringBackend struct {
	*backend
	recovered int
}

func (b *recoveringBackend) RecoverEffects(context.Context, state.EnvironmentGitOpsLease) error {
	b.recovered++
	return nil
}

func TestWorkerReportNeverRecoversEnforceEffects(t *testing.T) {
	for _, mode := range []string{"report", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			store, source, _, base, worker := setup(t, mode)
			base.observation.State.Fields[1].Value = json.RawMessage(`"console-edit"`)
			b := &recoveringBackend{backend: base}
			worker.Backend, worker.Mode = b, mode
			if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
				t.Fatalf("attempt: %v %v", worked, err)
			}
			if mode == "report" && (b.recovered != 0 || b.applied != 0 || lastRun(t, store, source).Status != "drifted") {
				t.Fatalf("report attempted mutation or hid drift: %+v", b)
			}
			if mode == "enforce" && (b.recovered != 1 || b.applied != 1) {
				t.Fatalf("enforce recovery was lost: %+v", b)
			}
		})
	}
}

// Deliberately exposes the older store interface without mode claims.
type unfilteredStore struct {
	state.EnvironmentGitOpsStore
}

func TestWorkerReportRequiresAtomicModeClaims(t *testing.T) {
	for _, mode := range []string{"report", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			store, source, _, b, worker := setup(t, "enforce")
			worker.Store, worker.Mode = unfilteredStore{store}, mode
			if worked, err := worker.RunOnce(t.Context()); err == nil || worked || b.observed != 0 || b.applied != 0 {
				t.Fatalf("unsafe claim fallback: %v %v %+v", worked, err, b)
			}
			runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
			if err != nil || len(runs) != 0 {
				t.Fatalf("enforce job was claimed: %+v %v", runs, err)
			}
		})
	}
}
