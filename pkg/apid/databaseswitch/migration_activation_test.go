package databaseswitch_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeMigrationTarget struct {
	h       *harness
	sealErr error
	openErr error
}

func (f *fakeMigrationTarget) SealMigrationTarget(_ context.Context, c managedpostgres.Cutover, operation string) error {
	if c.ID != f.h.sw.ID || operation != f.h.sw.MigrationOperationID {
		f.h.t.Fatalf("unexpected seal: %s %s", c.ID, operation)
	}
	f.h.cutovers.calls = append(f.h.cutovers.calls, "seal")
	return f.sealErr
}

func (f *fakeMigrationTarget) OpenMigrationTarget(_ context.Context, account, id, target, operation string) error {
	sw, c := f.h.sw, f.h.cutovers
	if c.cutover.State != managedpostgres.CutoverActivated || !c.fenced || c.resumed ||
		account != sw.AccountID || id != sw.ID || target != sw.TargetDatabaseID || operation != sw.MigrationOperationID ||
		c.calls[len(c.calls)-1] != "revoke" {
		f.h.t.Fatalf("target attempted to open out of order: %v", c.calls)
	}
	c.calls = append(c.calls, "open")
	return f.openErr
}

func (f *fakeMigrationTarget) AbortMigrationTarget(_ context.Context, _, _, _, _ string) error {
	if f.h.cutovers.cutover.State != managedpostgres.CutoverCancelled {
		f.h.t.Fatal("guest abort preceded durable cutover cancellation")
	}
	f.h.cutovers.calls = append(f.h.cutovers.calls, "abort-seal")
	return nil
}

func newMigrationHarness(t *testing.T) (*harness, *fakeMigrationTarget) {
	t.Helper()
	h := newHarnessWithSwitch(t, true, func(h *harness, sw *state.DatabaseSwitch) {
		h.databases.target.State = managedpostgres.StateReady
		sw.ID, sw.MigrationOperationID = uuid.NewString(), uuid.NewString()
		sw.RestorePoint, sw.TargetDatabaseID = time.Time{}, h.databases.target.ID
	})
	target := &fakeMigrationTarget{h: h}
	h.worker.MigrationTargets = target
	if h.sw.Phase != state.DatabaseSwitchStaging {
		t.Fatalf("initial migration phase = %s", h.sw.Phase)
	}
	return h, target
}

func TestMigrationActivationRecoversCommittedSwitchBeforeOpening(t *testing.T) {
	h, target := newMigrationHarness(t)
	h.runUntil(state.DatabaseSwitchDraining, 12)
	if got := h.cutovers.calls[:3]; !slices.Equal(got, []string{"prepare", "seal", "verify"}) {
		t.Fatalf("credential verification order: %v", got)
	}
	h.completeDrain()
	h.cutovers.activateErr = errors.New("activation reply lost")
	h.cutovers.committed = true
	h.runUntil(state.DatabaseSwitchSwitching, 5)
	h.tick()
	if sw := h.tick(); sw.Phase != state.DatabaseSwitchSwitching || h.cutovers.cutover.State != managedpostgres.CutoverActivated || h.cutovers.resumed {
		t.Fatalf("lost reply state = %s, catalog = %s, app resumed = %v", sw.Phase, h.cutovers.cutover.State, h.cutovers.resumed)
	}
	restarted := *h.worker
	h.worker = &restarted
	h.runUntil(state.DatabaseSwitchResuming, 5)
	target.openErr = errors.New("target control unavailable")
	h.tick()
	if h.sw.Phase != state.DatabaseSwitchResuming || !h.cutovers.fenced || h.cutovers.resumed {
		t.Fatalf("app resumed before the target opened: %v", h.cutovers.calls)
	}
	target.openErr = nil
	h.runUntil(state.DatabaseSwitchCompleted, 6)
	calls := h.cutovers.calls
	if want := []string{"seal", "activate", "revoke", "open", "revoke", "open", "resume"}; !slices.Equal(calls[len(calls)-len(want):], want) {
		t.Fatalf("activation order: %v", calls)
	}
	if h.releases.live["default"] != h.sw.FromDeploymentID || h.databases.restores != 0 || len(h.releases.marked) != 0 {
		t.Fatal("migration replaced the release or restored a database")
	}
}

func TestMigrationActivationCancelsBeforeChangingBindings(t *testing.T) {
	h, target := newMigrationHarness(t)
	h.runUntil(state.DatabaseSwitchDraining, 12)
	h.completeDrain()
	target.sealErr = managedpostgres.ErrConflict
	sw := h.runUntil(state.DatabaseSwitchFailed, 8)
	calls := h.cutovers.calls
	if sw.FailureCode != "migration_seal_refused" || slices.Contains(calls, "activate") || slices.Contains(calls, "open") || !slices.Contains(calls, "abort-seal") {
		t.Fatalf("failure code = %s, calls = %v", sw.FailureCode, calls)
	}
}
