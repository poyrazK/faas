// adr: 468 — delayed boots/resumes cannot bypass the durable app fence.
package fcvm

import (
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func admissionWake(instance string) WakeRequest {
	return WakeRequest{Instance: instance, AppID: "app-fenced", BaseKey: "base.ext4", LayerKey: "app.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanHobby}
}

func waitInstanceResult(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("instance operation did not finish")
		return ctx.Err()
	}
}

func liveAdmissionOperation(ctx context.Context, m *Manager, instance, kind string) error {
	switch kind {
	case "resume":
		return m.ResumeVM(ctx, instance)
	case "warm_snapshot", "warm_snapshot_resume":
		_, err := m.WarmSnapshot(ctx, instance, SnapshotSpec{})
		return err
	default:
		_, err := m.SnapshotKeepAlive(ctx, instance, SnapshotSpec{})
		return err
	}
}

func TestAppAdmissionRejectsBootBeforeResources(t *testing.T) {
	for _, guardErr := range []error{ErrAppAdmissionFenced, ErrAppAdmissionUnavailable} {
		for _, kind := range []string{"cold", "restore", "warm", "app_task", "builder"} {
			t.Run(guardErr.Error()+"/"+kind, func(t *testing.T) {
				run, v := &fakeRunner{}, &fakeVMM{}
				m := newTestManager(run, v).WithAppAdmissionGuard(func(context.Context, string) error { return guardErr })
				req := admissionWake("rejected")
				switch kind {
				case "restore", "warm":
					req.Snapshot, req.KeepPaused = usableSnapshot(), kind == "warm"
				case "app_task":
					req.AppTaskOnly = true
				case "builder":
					req.ExportDir = t.TempDir()
				}
				hookCalled := false
				_, err := m.WakeWithNetworkReady(t.Context(), req, func(WakeNetworkReady) {
					hookCalled = true
				})
				if !errors.Is(err, guardErr) {
					t.Fatalf("boot error = %v", err)
				}
				if hookCalled || len(run.commands) != 0 || v.bootCount != 0 || len(v.restored) != 0 || m.alloc.InUse() != 0 || m.LiveCount() != 0 || len(m.instanceFlights) != 0 {
					t.Fatal("rejected boot acquired resources or entered the VMM")
				}
			})
		}
	}
}

func TestAppAdmissionSurvivesDestroyAndManagerRestart(t *testing.T) {
	var fenced atomic.Bool
	guard := func(_ context.Context, appID string) error {
		if appID == "app-fenced" && fenced.Load() {
			return ErrAppAdmissionFenced
		}
		return nil
	}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithAppAdmissionGuard(guard)
	req := admissionWake("old-source")
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	fenced.Store(true)
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
	for _, manager := range []*Manager{m, newTestManager(&fakeRunner{}, &fakeVMM{}).WithAppAdmissionGuard(guard)} {
		for _, instance := range []string{req.Instance, "another-source"} {
			if _, err := manager.Wake(t.Context(), admissionWake(instance)); !errors.Is(err, ErrAppAdmissionFenced) {
				t.Fatalf("delayed boot after destroy/restart = %v", err)
			}
		}
		other := admissionWake("other-app")
		other.AppID = "app-other"
		if _, err := manager.Wake(t.Context(), other); err != nil {
			t.Fatalf("cross-app boot blocked: %v", err)
		}
		if err := manager.Destroy(t.Context(), other.Instance); err != nil {
			t.Fatal(err)
		}
	}
	fenced.Store(false)
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatalf("completed cancellation did not reopen admission: %v", err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}

func TestAppAdmissionRejectsLiveResumeAndCapture(t *testing.T) {
	for _, kind := range []string{"resume", "warm_snapshot", "migration_snapshot"} {
		for _, guardErr := range []error{ErrAppAdmissionFenced, ErrAppAdmissionUnavailable} {
			t.Run(kind+"/"+guardErr.Error(), func(t *testing.T) {
				v := &fakeVMM{}
				m := newTestManager(&fakeRunner{}, v)
				req := admissionWake("paused-source")
				if _, err := m.Wake(t.Context(), req); err != nil {
					t.Fatal(err)
				}
				m.WithAppAdmissionGuard(func(context.Context, string) error { return guardErr })
				if err := liveAdmissionOperation(t.Context(), m, req.Instance, kind); !errors.Is(err, guardErr) {
					t.Fatalf("live operation error = %v", err)
				}
				if len(v.resumed) != 0 || len(v.keepAliveSnapshotted) != 0 || m.LiveCount() != 1 || len(m.instanceFlights) != 0 {
					t.Fatal("fenced live operation touched the guest or lost cleanup identity")
				}
				if err := m.Destroy(t.Context(), req.Instance); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestAppAdmissionReadIsInsideDestructionJoin(t *testing.T) {
	for _, kind := range []string{"boot", "resume", "warm_snapshot", "migration_snapshot"} {
		for _, stop := range []string{"destroy", "signal"} {
			t.Run(kind+"/"+stop, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				v := &fakeVMM{}
				m := newTestManager(&fakeRunner{}, v)
				req := admissionWake("read-before-fence")
				if kind != "boot" {
					if _, err := m.Wake(ctx, req); err != nil {
						t.Fatal(err)
					}
				}
				entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				m.WithAppAdmissionGuard(func(ctx context.Context, _ string) error {
					close(entered)
					<-ctx.Done()
					close(cancelled)
					<-release // stale successful read ignores cancellation until released
					return nil
				})
				opErr := make(chan error, 1)
				go func() {
					if kind == "boot" {
						_, err := m.Wake(ctx, req)
						opErr <- err
						return
					}
					opErr <- liveAdmissionOperation(ctx, m, req.Instance, kind)
				}()
				waitBootSignal(t, ctx, entered)
				stopErr := make(chan error, 1)
				go func() {
					if stop == "signal" {
						_, _, err := m.SignalAndKill(ctx, req.Instance, syscall.SIGTERM, time.Second)
						stopErr <- err
						return
					}
					stopErr <- m.Destroy(ctx, req.Instance)
				}()
				waitBootSignal(t, ctx, cancelled)
				select {
				case err := <-stopErr:
					t.Fatalf("stop acknowledged an unfinished admission read: %v", err)
				default:
				}
				close(release)
				if err := waitInstanceResult(t, ctx, opErr); !errors.Is(err, context.Canceled) {
					t.Fatalf("stale successful read entered VMM: %v", err)
				}
				if err := waitInstanceResult(t, ctx, stopErr); err != nil {
					t.Fatal(err)
				}
				if len(v.resumed) != 0 || len(v.keepAliveSnapshotted) != 0 || m.LiveCount() != 0 || m.alloc.InUse() != 0 {
					t.Fatal("cancelled admission leaked or resumed a VM")
				}
			})
		}
	}
}

type delayedResumeVMM struct {
	*fakeVMM
	entered, cancelled, release chan struct{}
	blockSnapshot               bool
}

func (v *delayedResumeVMM) wait(ctx context.Context) {
	close(v.entered)
	<-ctx.Done()
	close(v.cancelled)
	<-v.release
}

func (v *delayedResumeVMM) ResumeVM(ctx context.Context, lease Lease) error {
	v.wait(ctx)
	return v.fakeVMM.ResumeVM(ctx, lease)
}

func (v *delayedResumeVMM) SnapshotKeepAlive(ctx context.Context, lease Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	if v.blockSnapshot {
		v.wait(ctx)
		return SnapshotInfo{}, errors.New("snapshot failed after stop cancellation")
	}
	return v.fakeVMM.SnapshotKeepAlive(ctx, lease, spec)
}

func TestStopJoinsLateResumeAndSnapshotFailure(t *testing.T) {
	for _, kind := range []string{"resume", "warm_snapshot", "warm_snapshot_resume", "migration_snapshot"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			v := &delayedResumeVMM{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), blockSnapshot: kind != "resume" && kind != "warm_snapshot_resume"}
			m := newTestManager(&fakeRunner{}, v)
			req := admissionWake("late-resume")
			if _, err := m.Wake(ctx, req); err != nil {
				t.Fatal(err)
			}
			opErr := make(chan error, 1)
			go func() { opErr <- liveAdmissionOperation(ctx, m, req.Instance, kind) }()
			waitBootSignal(t, ctx, v.entered)
			stopCtx, stopCancel := context.WithTimeout(ctx, 20*time.Millisecond)
			defer stopCancel()
			if err := m.Destroy(stopCtx, req.Instance); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("stop acknowledged late resume: %v", err)
			}
			waitBootSignal(t, ctx, v.cancelled)
			if m.LiveCount() != 1 || m.alloc.InUse() != 1 {
				t.Fatal("timed-out stop released resources before the resume unwound")
			}
			if err := m.ResumeVM(ctx, req.Instance); err == nil {
				t.Fatal("duplicate resume entered while cancellation was pending")
			}
			close(v.release)
			if err := waitInstanceResult(t, ctx, opErr); !errors.Is(err, context.Canceled) {
				t.Fatalf("late operation error = %v", err)
			}
			if v.blockSnapshot && len(v.resumed) != 0 {
				t.Fatal("snapshot failure detached cancellation and resumed the source")
			}
			if err := m.Destroy(ctx, req.Instance); err != nil {
				t.Fatal(err)
			}
			if m.LiveCount() != 0 || m.alloc.InUse() != 0 || len(m.instanceFlights) != 0 {
				t.Fatal("resume retry cleanup leaked resources")
			}
		})
	}
}

type cancelledSnapshotVMM struct {
	*fakeVMM
	cancelRPC context.CancelFunc
}

func (v *cancelledSnapshotVMM) SnapshotKeepAlive(ctx context.Context, _ Lease, _ SnapshotSpec) (SnapshotInfo, error) {
	v.cancelRPC()
	return SnapshotInfo{}, ctx.Err()
}

func (v *cancelledSnapshotVMM) ResumeVM(ctx context.Context, lease Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return v.fakeVMM.ResumeVM(ctx, lease)
}

func TestMigrationSnapshotRecoversAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	v := &cancelledSnapshotVMM{fakeVMM: &fakeVMM{}, cancelRPC: cancel}
	m := newTestManager(&fakeRunner{}, v)
	req := admissionWake("recover-expired-rpc")
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SnapshotKeepAlive(ctx, req.Instance, SnapshotSpec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot failure = %v", err)
	}
	if len(v.resumed) != 1 || m.LiveCount() != 1 || len(m.instanceFlights) != 0 {
		t.Fatal("caller cancellation suppressed the live guest recovery")
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}

func TestAppAdmissionRequiresAppIdentityOnConfiguredNode(t *testing.T) {
	v, run := &fakeVMM{}, &fakeRunner{}
	m := newTestManager(run, v).WithAppAdmissionGuard(func(context.Context, string) error { return nil })
	req := admissionWake("missing-app-identity")
	req.AppID = ""
	if _, err := m.Wake(t.Context(), req); !errors.Is(err, ErrAppAdmissionUnavailable) {
		t.Fatalf("ordinary boot without identity = %v", err)
	}
	if m.alloc.InUse() != 0 || len(run.commands) != 0 || v.bootCount != 0 {
		t.Fatal("missing app identity bypassed admission")
	}
	m.live[req.Instance] = &Instance{Lease: Lease{Instance: req.Instance}}
	if err := m.ResumeVM(t.Context(), req.Instance); !errors.Is(err, ErrAppAdmissionUnavailable) {
		t.Fatalf("ordinary resume without identity = %v", err)
	}
	if len(v.resumed) != 0 {
		t.Fatal("missing app identity resumed a guest")
	}
	delete(m.live, req.Instance)
	req.ExecutionOnly = true
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatalf("app-less networkless execution lost its isolation path: %v", err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}
