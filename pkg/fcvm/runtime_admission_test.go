package fcvm

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"testing"
)

var errRuntimeFenceTest = errors.New("test runtime fence")

func TestRuntimeAdmissionBlocksBeforeHostWork(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm).WithRuntimeAuthorizer(func(context.Context, RuntimeAdmission) error { return errRuntimeFenceTest })
	if _, err := m.Wake(t.Context(), wakeReq("blocked", usableSnapshot())); !errors.Is(err, errRuntimeFenceTest) {
		t.Fatalf("fence: %v", err)
	}
	if len(run.commands) != 0 || vmm.boots() != 0 || vmm.restoredInstance("blocked") || m.LeasedCount() != 0 || len(m.LiveInstances()) != 0 {
		t.Fatal("denied wake allocated host resources")
	}
}

func TestRuntimeAdmissionRechecksEachWakeBoundary(t *testing.T) {
	for _, test := range []struct {
		name         string
		snap         *Snapshot
		denyAt       int
		restoreFails bool
		wantBoots    int
		wantRestore  bool
	}{
		{"before-restore", usableSnapshot(), 2, false, 0, false},
		{"before-cold-boot", nil, 2, false, 0, false},
		{"restore-fallback", usableSnapshot(), 3, true, 0, true},
		{"before-restore-publication", usableSnapshot(), 3, false, 0, true},
		{"before-boot-publication", nil, 3, false, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			vmm := &fakeVMM{}
			if test.restoreFails {
				vmm.restoreErr = errors.New("restore failed")
			}
			calls := 0
			m := newTestManager(&fakeRunner{}, vmm).WithRuntimeAuthorizer(func(_ context.Context, r RuntimeAdmission) error {
				if r.Instance != "fenced" || r.AppID != "regional-app" {
					t.Fatalf("lost runtime identity: %+v", r)
				}
				calls++
				if calls >= test.denyAt {
					return errRuntimeFenceTest
				}
				return nil
			})
			req := wakeReq("fenced", test.snap)
			req.AppID = "regional-app"
			if _, err := m.Wake(t.Context(), req); !errors.Is(err, errRuntimeFenceTest) {
				t.Fatalf("fence: %v", err)
			}
			if vmm.boots() != test.wantBoots || vmm.restoredInstance("fenced") != test.wantRestore {
				t.Fatalf("host calls: boots=%d restore=%v", vmm.boots(), vmm.restoredInstance("fenced"))
			}
			if m.LeasedCount() != 0 || len(m.LiveInstances()) != 0 {
				t.Fatal("denied publication leaked a lease or live VM")
			}
		})
	}
}

type quotaSnapshotVMM struct {
	*fakeVMM
	earlyChecks int
}

func (v *quotaSnapshotVMM) SnapshotKeepAlive(ctx context.Context, lease Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	if spec.ResumeBeforePublish {
		if spec.BeforeResume == nil {
			return SnapshotInfo{}, errors.New("missing early-resume authority")
		}
		v.earlyChecks++
		if err := spec.BeforeResume(ctx); err != nil {
			return SnapshotInfo{}, err
		}
		if err := v.ResumeVM(ctx, lease); err != nil {
			return SnapshotInfo{}, err
		}
	}
	return v.fakeVMM.SnapshotKeepAlive(ctx, lease, spec)
}

func TestRuntimeAdmissionResumeAndSnapshotRecovery(t *testing.T) {
	for _, test := range []string{"migration-or-warm-resume", "warm-early-resume", "migration-snapshot-error"} {
		t.Run(test, func(t *testing.T) {
			vmm := &quotaSnapshotVMM{fakeVMM: &fakeVMM{}}
			if test == "migration-snapshot-error" {
				vmm.snapErr = errors.New("capture failed")
			}
			m := newTestManager(&fakeRunner{}, vmm).WithRuntimeAuthorizer(func(_ context.Context, r RuntimeAdmission) error {
				if r.AppID != "regional-app" || r.Instance != "resident" {
					t.Fatalf("resident identity: %+v", r)
				}
				return errRuntimeFenceTest
			})
			m.RegisterInstanceForTest("resident", "deployment", "regional-app", "account")
			m.mu.Lock()
			m.live["resident"].Lease.Instance = "resident"
			m.mu.Unlock()
			var err error
			switch test {
			case "migration-or-warm-resume":
				err = m.ResumeVM(t.Context(), "resident")
			case "warm-early-resume":
				_, err = m.WarmSnapshot(t.Context(), "resident", SnapshotSpec{})
			default:
				_, err = m.SnapshotKeepAlive(t.Context(), "resident", SnapshotSpec{})
			}
			if !errors.Is(err, errRuntimeFenceTest) {
				t.Fatalf("resume fence: %v", err)
			}
			if len(vmm.resumed) != 0 {
				t.Fatal("fenced resident resumed")
			}
			if len(m.LiveInstances()) != 1 {
				t.Fatal("fence hid resident capacity before confirmed teardown")
			}
			if test == "warm-early-resume" && vmm.earlyChecks != 1 {
				t.Fatal("warm snapshot skipped early resume authorization")
			}
		})
	}
}

func TestRuntimeAdmissionFencedAppTaskNeverReceivesCommand(t *testing.T) {
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm).WithRuntimeAuthorizer(func(_ context.Context, r RuntimeAdmission) error {
		if !r.AppTaskOnly || !r.Command || r.AppID != "regional-app" {
			t.Fatalf("command identity: %+v", r)
		}
		return errRuntimeFenceTest
	})
	m.RegisterInstanceForTest("task", "deployment", "regional-app", "account")
	m.mu.Lock()
	m.live["task"].Lease.Instance = "task"
	m.live["task"].AppTaskOnly = true
	m.mu.Unlock()
	req := apptaskproto.Request{Version: apptaskproto.Version, TaskID: "task", Command: []string{"echo", "run"}, TimeoutSeconds: 30, MaxOutputBytes: 1024}
	if _, err := m.ExecuteAppTask(t.Context(), "task", req); !errors.Is(err, errRuntimeFenceTest) {
		t.Fatalf("command fence: %v", err)
	}
	if len(m.LiveInstances()) != 0 || len(vmm.killed) != 1 {
		t.Fatal("fenced app-task VM was not destroyed")
	}
}
