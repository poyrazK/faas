package fcvm

// adr: 628
import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

const regionalMigrationToken = "0123456789abcdef0123456789abcdef"

func TestRegionalMigrationCaptureAndRollbackKeepOriginalPhysicalAttempt(t *testing.T) {
	for _, failedCapture := range []bool{false, true} {
		t.Run(map[bool]string{false: "capture-completed", true: "uncertain-capture"}[failedCapture], func(t *testing.T) {
			v := &fakeVMM{}
			m, j, req := regionalHostFixture(t, v)
			inst, err := m.Wake(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("capture outcome uncertain")
			if failedCapture {
				v.snapErr = failure
			}
			_, err = m.SnapshotKeepAlive(t.Context(), req.Instance, SnapshotSpec{MigrationLeaseToken: regionalMigrationToken, RegionalMigrationSource: req.RegionalRuntime, StorageKey: "snap/source/mem", VMStateStorageKey: "snap/source/vmstate"})
			if failedCapture && !errors.Is(err, failure) || !failedCapture && err != nil {
				t.Fatal(err)
			}
			if _, ok := m.NetnsFor(req.Instance); ok || len(v.resumed) != 0 {
				t.Fatal("capture exposed source or resumed implicitly")
			}
			r, err := j.MigrationSource(req.Instance, regionalMigrationToken)
			if err != nil || r.Source.Permit != req.RegionalRuntime.Permit {
				t.Fatal("capture did not retain source", err)
			}
			p := req.RegionalRuntime.Clone()
			p.Seat.ID = uuid.NewString()
			if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); !errors.Is(err, runtimequotahost.ErrBinding) {
				t.Fatal("warm resume bypassed migration", err)
			}
			v.resumeErr = failure
			if err := m.ResumeRegionalMigration(t.Context(), req.Instance, regionalMigrationToken, p); !errors.Is(err, failure) {
				t.Fatal("uncertain native resume acknowledged", err)
			}
			other := p.Clone()
			other.Seat.ID = uuid.NewString()
			if err := m.ResumeRegionalMigration(t.Context(), req.Instance, regionalMigrationToken, other); !errors.Is(err, runtimequotahost.ErrBinding) {
				t.Fatal("retry changed seat", err)
			}
			v.resumeErr = nil
			if err := m.ResumeRegionalMigration(t.Context(), req.Instance, regionalMigrationToken, p); err != nil {
				t.Fatal(err)
			}
			if err := m.ResumeRegionalMigration(t.Context(), req.Instance, regionalMigrationToken, p); err != nil {
				t.Fatal("lost reply replay failed", err)
			}
			if len(v.resumed) != 2 || inst.Paused || admissionForInstance(inst).RegionalResumePending {
				t.Fatal("completion replay repeated native resume or failed publication")
			}
			r, err = j.MigrationSource(req.Instance, regionalMigrationToken)
			if err != nil || !r.Completed || r.Source.Permit != p.Permit || r.Source.Seat.ID != req.RegionalRuntime.Seat.ID {
				t.Fatal("completion lost original source", err)
			}
			if err := m.Destroy(t.Context(), req.Instance); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRegionalMigrationUnfinishedRestartCannotInventCompletion(t *testing.T) {
	v := &fakeVMM{}
	m, j, req := regionalHostFixture(t, v)
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SnapshotKeepAlive(t.Context(), req.Instance, SnapshotSpec{MigrationLeaseToken: regionalMigrationToken, RegionalMigrationSource: req.RegionalRuntime, StorageKey: "snap/source/mem", VMStateStorageKey: "snap/source/vmstate"}); err != nil {
		t.Fatal(err)
	}
	p := req.RegionalRuntime.Clone()
	p.Seat.ID = uuid.NewString()
	// Simulate restart's empty serving map using the surviving journal. Real
	// recovered native processes remain quarantined, never inferred serving.
	restarted := newTestManager(&fakeRunner{}, v).WithRuntimeHostJournal(j).WithRuntimeAuthorizer(func(context.Context, RuntimeAdmission) error { return nil })
	if err := restarted.ResumeRegionalMigration(t.Context(), req.Instance, regionalMigrationToken, p); err == nil {
		t.Fatal("empty live map completed unfinished rollback")
	}
	if len(v.resumed) != 0 {
		t.Fatal("unfinished recovered source resumed")
	}
	if err := m.ResumeRegionalMigration(t.Context(), req.Instance, regionalMigrationToken, p); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ResumeRegionalMigration(t.Context(), req.Instance, regionalMigrationToken, p); err != nil {
		t.Fatal("durable known completion could not replay", err)
	}
	if len(v.resumed) != 1 {
		t.Fatal("known completion repeated native work")
	}
	if _, err := j.FenceSeat(*p.Seat, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ResumeRegionalMigration(t.Context(), req.Instance, regionalMigrationToken, p); !errors.Is(err, runtimequotahost.ErrFenced) {
		t.Fatal("completion reopened fence", err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}
