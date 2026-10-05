//go:build linux || darwin

package fcvm

// adr: 630
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

// Simulated native process/resource backends verify ordering and identity.
// These fixtures supply no KVM, kernel resource or native acceptance evidence.
func regionalNativeStopFixture(t *testing.T, compact bool) (*Manager, *JailerVMM, *runtimequotahost.Journal, WakeRequest, Lease) {
	t.Helper()
	m, v, host, req := nativeRegionalManagerFixture(t)
	if compact {
		req.Instance = strings.ReplaceAll(req.Instance, "-", "")
	}
	if err := m.preflightNativeRegionalRuntime(t.Context(), req.RegionalRuntime); err != nil {
		t.Fatal(err)
	}
	finish, err := host.BeginLaunch(t.Context(), *req.RegionalRuntime)
	if err != nil {
		t.Fatal(err)
	}
	finish()
	lease, err := m.alloc.Acquire(req.Instance)
	if err != nil {
		t.Fatal(err)
	}
	lease.Plan, lease.MemoryMaxMiB = req.Plan, req.MemSizeMiB
	if err := v.prepareNativeRegionalLease(t.Context(), lease, *req.RegionalRuntime); err != nil {
		t.Fatal(err)
	}
	return m, v, host, req, lease
}

func regionalStopProcess(t *testing.T, v *JailerVMM, lease Lease) (string, *recoveryHandle) {
	t.Helper()
	dir := writeNativeRecoveryProcess(t, v.nativeRecovery.retirer.probe.root, 42, lease.Instance, lease.UID, 101)
	if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::/"+ParentCgroupFor(lease.Plan)+"/"+lease.Instance+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ticket, err := v.nativeRecovery.journal.beginLaunch(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(ticket.authorize(42, 101), ticket.close()); err != nil {
		t.Fatal(err)
	}
	handle := &recoveryHandle{onWait: func(context.Context) error { return os.RemoveAll(dir) }}
	v.nativeRecovery.retirer.open = func(pid int) (nativeProcessHandle, error) {
		if pid != 42 {
			t.Error("retirement pinned a different numeric PID")
		}
		return handle, nil
	}
	return dir, handle
}

func TestRegionalNativeStopRetainsOriginalFenceAndVerifiesRetry(t *testing.T) {
	for _, scenario := range []string{"prepared", "running", "paused", "deleted-row", "compact-key"} {
		t.Run(scenario, func(t *testing.T) {
			m, v, host, req, lease := regionalNativeStopFixture(t, scenario == "compact-key")
			var handle *recoveryHandle
			if scenario != "prepared" {
				_, handle = regionalStopProcess(t, v, lease)
			}
			record, err := v.nativeRecovery.journal.read(req.Instance)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "running" || scenario == "paused" {
				m.live[req.Instance] = &Instance{Lease: lease, Net: nativeLeaseNetwork(lease), nativeGeneration: record.Generation, Paused: scenario == "paused", regionalRuntime: cloneRuntimePlacement(req.RegionalRuntime)}
			}
			m.WithRuntimeAuthorizer(func(context.Context, RuntimeAdmission) error {
				return errors.New("cell row was deleted or admission retired")
			})
			fence := uuid.NewString()
			for attempt := range 2 {
				requested := fence
				if attempt != 0 {
					requested = uuid.NewString()
				}
				stop, err := m.RetireRegionalRuntime(t.Context(), req.Instance, req.RegionalRuntime.Permit, requested)
				if err != nil || stop.Validate() != nil || stop.Permit != req.RegionalRuntime.Permit || stop.FenceID != fence {
					t.Fatal("original stop/fence was not conserved", err)
				}
			}
			if handle != nil && (!handle.waited || !handle.closed || len(handle.signals) != 1 || handle.signals[0] != syscall.SIGKILL) {
				t.Fatal("native stop did not pin and join the original incarnation")
			}
			r, err := v.nativeRecovery.journal.read(req.Instance)
			if err != nil || !r.Revoked || !r.ExitConfirmed || !r.ResourcesRemoved || !sameOptionalRuntimePlacement(r.RegionalRuntime, req.RegionalRuntime) || m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("retirement lost original ownership or retained Manager resources", err)
			}
			rows, err := host.PermitInventory()
			if err != nil || len(rows) != 1 || rows[0].FenceID != fence || !rows[0].Registered {
				t.Fatal("host retirement deleted ledger ownership", err)
			}
		})
	}
}

func TestRegionalNativeStopRefusesMissingOrConflictingOriginalProvenance(t *testing.T) {
	for _, scenario := range []string{"no-native", "no-host", "wrong-key", "wrong-node", "wrong-epoch", "wrong-permit", "missing-frame", "unbound-frame", "different-boot", "live-generation", "live-lease", "cleanup-lease"} {
		t.Run(scenario, func(t *testing.T) {
			m, v, host, req, lease := regionalNativeStopFixture(t, false)
			p := req.RegionalRuntime.Permit
			r, err := v.nativeRecovery.journal.read(req.Instance)
			if err != nil {
				t.Fatal(err)
			}
			instance := req.Instance
			switch scenario {
			case "no-native":
				m.vmm = &fakeVMM{}
			case "no-host":
				m.runtimeHostJournal = nil
			case "wrong-key":
				instance = uuid.NewString()
			case "wrong-node":
				p.NodeID = uuid.NewString()
			case "wrong-epoch":
				p.HostEpochID = uuid.NewString()
			case "wrong-permit":
				p.RunID = uuid.NewString()
			case "missing-frame":
				if err := os.Remove(v.nativeRecovery.journal.path(req.Instance)); err != nil {
					t.Fatal(err)
				}
			case "unbound-frame":
				r.Version, r.RegionalRuntime = 1, nil
				if err := v.nativeRecovery.journal.write(r); err != nil {
					t.Fatal(err)
				}
			case "different-boot":
				v.nativeRecovery.journal.bootID = func() (string, error) { return uuid.NewString(), nil }
			case "live-generation", "live-lease":
				inst := &Instance{Lease: lease, nativeGeneration: r.Generation}
				if scenario == "live-generation" {
					inst.nativeGeneration = uuid.NewString()
				} else {
					inst.Lease.MemoryMaxMiB++
				}
				m.live[req.Instance] = inst
			case "cleanup-lease":
				changed := lease
				changed.MemoryMaxMiB++
				m.retainCleanup(changed, nativeLeaseNetwork(changed), nil)
			}
			stop, err := m.RetireRegionalRuntime(t.Context(), instance, p, uuid.NewString())
			if err == nil || stop != (runtimequota.CapacityStop{}) || m.LeasedCount() != 1 {
				t.Fatal("missing or conflicting original ownership acknowledged retirement", err)
			}
			rows, err := host.PermitInventory()
			if err != nil || len(rows) != 1 || rows[0].Permit != req.RegionalRuntime.Permit {
				t.Fatal("failure dropped the original physical hold", err)
			}
			if len(m.run.(*fakeRunner).commands) != 0 {
				t.Fatal("binding failure reached network cleanup")
			}
		})
	}
}

func TestRegionalNativeStopUncertainExitRetainsResourcesAndCanRetry(t *testing.T) {
	m, v, _, req, lease := regionalNativeStopFixture(t, false)
	dir, handle := regionalStopProcess(t, v, lease)
	cause := errors.New("pinned native exit is uncertain")
	handle.onWait = func(context.Context) error { return cause }
	stop, err := m.RetireRegionalRuntime(t.Context(), req.Instance, req.RegionalRuntime.Permit, uuid.NewString())
	if !errors.Is(err, cause) || stop != (runtimequota.CapacityStop{}) || m.LeasedCount() != 1 || m.pendingCleanup[req.Instance] == nil || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("uncertain exit acknowledged absence or released resources", err)
	}
	handle.onWait = func(context.Context) error { return os.RemoveAll(dir) }
	if stop, err := m.RetireRegionalRuntime(t.Context(), req.Instance, req.RegionalRuntime.Permit, uuid.NewString()); err != nil || stop.Validate() != nil || m.LeasedCount() != 0 {
		t.Fatal("exact pending cleanup could not retry", err)
	}
}

func TestRegionalNativeStopRequiresExclusiveNativeDaemonOwnership(t *testing.T) {
	m, v, host, req, _ := regionalNativeStopFixture(t, false)
	r := v.nativeRecovery
	if err := r.daemonLock.Close(); err != nil {
		t.Fatal(err)
	}
	r.daemonLock = nil
	m.nativeRecoveryReady = false
	otherOwner, err := lockNativeJournalFile(t.Context(), filepath.Join(r.journal.root, ".daemon-owner.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer otherOwner.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	stop, err := m.RetireRegionalRuntime(ctx, req.Instance, req.RegionalRuntime.Permit, uuid.NewString())
	if !errors.Is(err, context.DeadlineExceeded) || stop != (runtimequota.CapacityStop{}) || m.LeasedCount() != 1 || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("another daemon's original native ownership was bypassed", err)
	}
	rows, err := host.PermitInventory()
	if err != nil || len(rows) != 1 || rows[0].FenceID != "" {
		t.Fatal("failed native ownership acquisition reached host fencing", err)
	}
}

func TestRegionalNativeStopRechecksResourcesAfterDurableAcknowledgement(t *testing.T) {
	for _, scenario := range []string{"resource-reappeared", "process-reappeared", "closed-host", "scan-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, v, host, req, lease := regionalNativeStopFixture(t, false)
			cause := errors.New("resource absence is uncertain")
			calls := 0
			v.nativeRecovery.resources = func(_ Lease, _ netns.Config) error {
				calls++
				if calls == 1 {
					switch scenario {
					case "process-reappeared":
						writeNativeRecoveryProcess(t, v.nativeRecovery.retirer.probe.root, 43, req.Instance, lease.UID, 202)
					case "scan-unavailable":
						v.nativeRecovery.retirer.probe.root = filepath.Join(t.TempDir(), "missing")
					}
				}
				if calls > 1 {
					if scenario == "resource-reappeared" {
						return cause
					}
					if scenario == "closed-host" {
						return host.Close()
					}
				}
				return nil
			}
			stop, err := m.RetireRegionalRuntime(t.Context(), req.Instance, req.RegionalRuntime.Permit, uuid.NewString())
			if err == nil || stop != (runtimequota.CapacityStop{}) {
				t.Fatal("prior native acknowledgement substituted for current absence", err)
			}
			r, err := v.nativeRecovery.journal.read(req.Instance)
			if err != nil || !r.ResourcesRemoved {
				t.Fatal("fixture did not reach the prior durable resource acknowledgement", err)
			}
		})
	}
}

type regionalLateBootRunner struct{ entered chan struct{} }

func (r *regionalLateBootRunner) Run(ctx context.Context, argv []string) error {
	if len(argv) > 1 && argv[0] == "ip" && argv[1] == "-batch" {
		close(r.entered)
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func TestRegionalNativeStopJoinsLateBootBeforeReturningAssertion(t *testing.T) {
	m, _, _, req := nativeRegionalManagerFixture(t)
	runner := &regionalLateBootRunner{entered: make(chan struct{})}
	m.run = runner
	wakeDone := make(chan error, 1)
	go func() { _, err := m.Wake(t.Context(), req); wakeDone <- err }()
	select {
	case <-runner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("boot did not reach the host boundary")
	}
	stop, err := m.RetireRegionalRuntime(t.Context(), req.Instance, req.RegionalRuntime.Permit, uuid.NewString())
	if err != nil || stop.Validate() != nil {
		t.Fatal("late boot was not joined and retired", err)
	}
	if err := <-wakeDone; !errors.Is(err, context.Canceled) {
		t.Fatal("late boot published after permit fencing", err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || len(m.cidToID) != 0 {
		t.Fatal("late boot remained published or allocated")
	}
}
