package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFunctionExecutionParkBusyKeepsRunningAndCapacity(t *testing.T) {
	f := newFrozenFixtureWith(t, true, func(app *state.App) { app.Runtime = "node22" })
	f.vmm.freezeErr = api.NewProblem(409, api.CodeInstanceBusy, "Function executing", "registered execution has not completed")
	resident := f.e.Ledger().ResidentRAM()
	_, err := f.e.freezeFunctionForPark(t.Context(), f.ins)
	if !functionExecutionBusyProblem(err) {
		t.Fatalf("execution admission: %v", err)
	}
	assertFunctionParkUnchanged(t, f, resident)
	if functionlifetime.NativeQualificationBuild {
		// Reusing an earlier snapshot must pass the same admission barrier
		// before the shortcut can destroy the current VM.
		if err := f.e.Park(t.Context(), f.ins.ID); !functionExecutionBusyProblem(err) {
			t.Fatalf("reusable snapshot bypassed idle admission: %v", err)
		}
		assertFunctionParkUnchanged(t, f, resident)
	}
}

func TestFunctionExecutionParkRollbackIsIncarnationScoped(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same", true: "terminal"}[changed], func(t *testing.T) {
			f := newFrozenFixture(t, false)
			ctx, cancel := context.WithCancel(t.Context())
			undo, err := f.e.freezeFunctionForPark(ctx, f.ins)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				if moved, err := f.e.transitionWithKindCAS(t.Context(), f.ins.ID, f.ins.AppID, state.StateStopped, "retired", "test"); err != nil || !moved {
					t.Fatal(err)
				}
			}
			cancel()
			undo()
			want := 1
			if changed {
				want = 0
			}
			if f.vmm.thaws != want {
				t.Fatalf("thaws=%d want=%d", f.vmm.thaws, want)
			}
		})
	}
}

func TestFunctionExecutionWarmBusyPreservesOwnership(t *testing.T) {
	f := newFrozenFixtureWith(t, false, func(app *state.App) { app.WarmSnapshotEnabled = true })
	ready := time.Now().Add(-time.Minute)
	f.ins.FrameworkReadyAt = &ready
	f.vmm.warmSnapErr = api.NewProblem(409, api.CodeInstanceBusy, "Function executing", "registered execution has not completed")
	resident := f.e.Ledger().ResidentRAM()
	if _, err := f.e.captureWarmSnapshotLocked(t.Context(), f.ins, f.app); !functionExecutionBusyProblem(err) {
		t.Fatalf("warm admission: %v", err)
	}
	assertFunctionParkUnchanged(t, f, resident)
	if functionExecutionBusyProblem(errors.New("storage failed")) {
		t.Fatal("ordinary snapshot failure classified as busy")
	}
}

func assertFunctionParkUnchanged(t *testing.T, f *frozenFixture, resident int) {
	t.Helper()
	ins, err := f.store.InstanceByID(t.Context(), f.ins.ID)
	if err != nil || ins.State != string(state.StateRunning) || f.e.Ledger().ResidentRAM() != resident || f.vmm.destroys != 0 || f.vmm.snapshots != 0 {
		t.Fatalf("busy admission changed ownership: state=%s resident=%d destroys=%d snapshots=%d err=%v", ins.State, f.e.Ledger().ResidentRAM(), f.vmm.destroys, f.vmm.snapshots, err)
	}
}
