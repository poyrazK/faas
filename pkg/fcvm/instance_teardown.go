package fcvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/onebox-faas/faas/pkg/netns"
)

// A stop reservation closes the gap between joining an operation and finding
// its live identity. Other stops wait; boots and resumable operations fail closed.
type instanceStop = instanceTeardownFlight

func (m *Manager) beginInstanceStop(ctx context.Context, instance string) (*instanceStop, error) {
	for {
		m.mu.Lock()
		previous := m.instanceStops[instance]
		if previous == nil {
			stop := &instanceStop{done: make(chan struct{})}
			if m.instanceStops == nil {
				m.instanceStops = make(map[string]*instanceStop)
			}
			m.instanceStops[instance] = stop
			m.mu.Unlock()
			return stop, nil
		}
		m.mu.Unlock()
		select {
		case <-previous.done:
		case <-ctx.Done():
			return nil, fmt.Errorf("manager: wait for instance %s teardown: %w", instance, ctx.Err())
		}
	}
}

func (m *Manager) finishInstanceStop(instance string, stop *instanceStop) {
	m.mu.Lock()
	delete(m.instanceStops, instance)
	close(stop.done)
	m.mu.Unlock()
}

// Failed boot cleanup has no live Instance. Retain the full resource identity
// independently, so Destroy can retry and the allocator cannot recycle its slot.
type instanceCleanup struct {
	mu            sync.Mutex
	lease         Lease
	net           netns.Config
	workloadNames []string
	complete      bool
	// failed is set when a cleanup attempt returned an error. An entry is
	// also present while a teardown is still running (DestroyWithExport
	// retains it before waiting for the guest and exporting), and only
	// failed entries may be retried in the background.
	failed bool
}

func (m *Manager) retainCleanup(lease Lease, nc netns.Config, workloadNames []string) *instanceCleanup {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cidToID, GuestVsockCID(lease.Slot))
	if retained := m.pendingCleanup[lease.Instance]; retained != nil {
		return retained
	}
	if m.pendingCleanup == nil {
		m.pendingCleanup = make(map[string]*instanceCleanup)
	}
	retained := &instanceCleanup{lease: lease, net: nc, workloadNames: append([]string(nil), workloadNames...)}
	m.pendingCleanup[lease.Instance] = retained
	return retained
}

func (m *Manager) cleanup(ctx context.Context, lease Lease, nc netns.Config, workloadNames []string) error {
	retained := m.retainCleanup(lease, nc, workloadNames)
	retained.mu.Lock()
	defer retained.mu.Unlock()
	if retained.complete {
		return nil
	}
	forwards := m.retireHTTPForwards(lease)
	if err := m.cleanupOwned(ctx, retained, forwards); err != nil {
		retained.failed = true
		m.log.Warn("cleanup pending; retaining instance lease", "instance", lease.Instance, "err", err)
		return err
	}
	if v := m.nativeVMM(); v != nil {
		if err := v.confirmNativeCleanup(ctx, lease, nativeLeaseNetwork(lease)); err != nil {
			return err
		}
		if released, err := m.releaseNativeRecovered(lease.Instance); released || err != nil {
			if err != nil {
				return err
			}
			retained.complete = true
			m.mu.Lock()
			delete(m.pendingCleanup, lease.Instance)
			m.mu.Unlock()
			return nil
		}
	}
	if m.resourceJournal != nil {
		if err := m.resourceJournal.forget(lease); err != nil {
			return fmt.Errorf("cleanup %s: retire resource journal before release: %w", lease.Instance, err)
		}
	}
	m.mu.Lock()
	// A duplicate native stop is still audited against its durable receipt,
	// but its already-confirmed reservation may have been released earlier.
	releaseLease := true
	if m.nativeVMM() != nil {
		m.alloc.mu.Lock()
		_, releaseLease = m.alloc.byInstance[lease.Instance]
		m.alloc.mu.Unlock()
	}
	if releaseLease {
		if err := m.alloc.Release(lease.Instance); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("cleanup %s: release lease: %w", lease.Instance, err)
		}
	}
	retained.complete = true
	delete(m.pendingCleanup, lease.Instance)
	delete(m.pendingProcessExits, lease.Instance)
	delete(m.processGenerations, lease.Instance)
	delete(m.waking, lease.Instance)
	delete(m.live, lease.Instance)
	delete(m.cidToID, GuestVsockCID(lease.Slot))
	delete(m.exportDirs, lease.Instance)
	m.mu.Unlock()
	m.forgetHTTPForwards(forwards)
	if !lease.Networkless {
		m.rebuildHostSMTPAllowlistRules(context.WithoutCancel(ctx))
	}
	if m.diskMetrics != nil {
		m.diskMetrics.Delete(lease.Instance)
	}
	return nil
}

func (m *Manager) cleanupOwned(ctx context.Context, retained *instanceCleanup, forwards *httpForwardGeneration) error {
	lease, nc := retained.lease, retained.net
	// No network teardown or slot release before confirmed child exit. Kill
	// also removes the jail, image mounts, materialised files and cgroup scope.
	if err := m.vmm.Kill(ctx, lease); err != nil {
		return fmt.Errorf("cleanup %s: kill vm: %w", lease.Instance, err)
	}
	if err := m.waitHTTPForwards(ctx, forwards); err != nil {
		return fmt.Errorf("cleanup %s: forwarding drain: %w", lease.Instance, err)
	}
	if len(retained.workloadNames) > 0 {
		parent := ParentCgroupFor(lease.Plan)
		if lease.IsBuilder {
			parent = BuilderCgroupParent
		}
		removeWorkloadCgroups(filepath.Join(cgroupRoot, parent, PerInstanceScope(lease.Instance)), retained.workloadNames)
	}
	if !lease.Networkless {
		m.unregisterEgressCircuitNetwork(lease.Instance)
		if m.nativeVMM() != nil {
			networkCtx, skip, err := m.nativeCleanupNetworkContext(ctx, lease)
			if err != nil {
				return err
			}
			if !skip {
				for _, argv := range nativeLeaseNetwork(lease).TeardownCommands() {
					if err := m.runNetworkCommand(networkCtx, argv); err != nil {
						m.log.Debug("cleanup: native teardown command", "cmd", argv, "err", err)
					}
				}
			}
			return nil // confirmNativeCleanup owns physical absence validation above.
		}
		if m.resourceJournal != nil {
			return m.teardownJournalNetwork(ctx, nc)
		}
		if err := m.checkOwnedNamespace(nc.Netns); err != nil {
			return fmt.Errorf("cleanup %s: %w", lease.Instance, err)
		}
		for _, argv := range nc.TeardownCommands() {
			if err := m.run.Run(ctx, argv); err != nil {
				// Partial setup and repeated deletion legitimately fail. Check
				// the owned namespace/veth identities rather than exit status.
				m.log.Debug("cleanup: teardown cmd", "cmd", argv, "err", err)
			}
		}
		if err := networkRemoved(nc); err != nil {
			return fmt.Errorf("cleanup %s: %w", lease.Instance, err)
		}
		if err := m.retireOwnedNamespace(nc); err != nil {
			return err
		}
	}
	return nil
}

func networkRemoved(nc netns.Config) error {
	for _, resource := range []struct{ base, name string }{
		{"/run/netns", nc.Netns}, {"/sys/class/net", nc.VethHost},
		{"/sys/class/net", nc.PrivateVethHost},
	} {
		if resource.name == "" {
			continue
		}
		_, err := os.Lstat(filepath.Join(resource.base, resource.name))
		if err == nil {
			return fmt.Errorf("network resource %s survived teardown", resource.name)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("check network resource %s: %w", resource.name, err)
		}
	}
	return nil
}

func (m *Manager) joinForTeardown(ctx context.Context, instance string) error {
	m.mu.Lock()
	_, held := m.restartQuarantine[instance]
	m.mu.Unlock()
	if held {
		return fmt.Errorf("stop %s: %w", instance, ErrRestartQuarantine)
	}
	if err := m.cancelInFlightInstance(ctx, instance); err != nil {
		return err
	}
	m.DeleteLivenessConsecutiveFailures(instance)
	m.cancelLivenessLoop(instance)
	m.cancelReadinessLoop(instance)
	m.cancelFrameworkReadyLoop(instance)
	return nil
}

// RetryPendingCleanups re-runs teardown for every instance whose cleanup
// failed and is still retained. A failed teardown keeps its lease, slot and
// writable clone until some caller runs Destroy again, and for a parked
// instance nothing does: production-us hunt #4 found one retained since an
// `app restart` whose replacement briefly shared its bind source (ADR-631).
// cleanup is idempotent and serialised per instance, so a concurrent Destroy
// is safe.
//
// Only teardowns whose last attempt failed are retried. DestroyWithExport
// retains its instance before waiting for the guest and exporting its drive,
// and builderd holds that call open for the whole build; retrying such an
// in-flight entry killed running builder VMs and unmounted their drive under
// the export (production-us rc.244: every build failed with EBADMSG).
func (m *Manager) RetryPendingCleanups(ctx context.Context) (completed, pending int) {
	m.mu.Lock()
	retained := make([]*instanceCleanup, 0, len(m.pendingCleanup))
	for instance, r := range m.pendingCleanup {
		// A registered teardown flight owns this instance until it returns.
		if m.instanceStops[instance] != nil {
			continue
		}
		retained = append(retained, r)
	}
	m.mu.Unlock()
	for _, r := range retained {
		if ctx.Err() != nil {
			return completed, pending
		}
		// TryLock: an entry whose mutex is held is being torn down right
		// now; that caller reports its own outcome.
		if !r.mu.TryLock() {
			continue
		}
		retry := r.failed && !r.complete
		r.mu.Unlock()
		if !retry {
			continue
		}
		if err := m.cleanup(ctx, r.lease, r.net, r.workloadNames); err != nil {
			pending++
			continue
		}
		completed++
	}
	return completed, pending
}

func (m *Manager) teardownIdentity(instance string) *Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst := m.live[instance]; inst != nil {
		return inst
	}
	if retained := m.pendingCleanup[instance]; retained != nil {
		return &Instance{Lease: retained.lease, Net: retained.net, WorkloadNames: retained.workloadNames}
	}
	return nil
}
