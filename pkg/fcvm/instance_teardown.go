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
type instanceStop struct{ done chan struct{} }

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
}

func (m *Manager) retainCleanup(lease Lease, nc netns.Config, workloadNames []string) *instanceCleanup {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	if err := m.cleanupOwned(ctx, retained); err != nil {
		m.log.Warn("cleanup pending; retaining instance lease", "instance", lease.Instance, "err", err)
		return err
	}
	if m.resourceJournal != nil {
		if err := m.resourceJournal.forget(lease); err != nil {
			return fmt.Errorf("cleanup %s: retire resource journal before release: %w", lease.Instance, err)
		}
	}
	m.mu.Lock()
	if err := m.alloc.Release(lease.Instance); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("cleanup %s: release lease: %w", lease.Instance, err)
	}
	retained.complete = true
	delete(m.pendingCleanup, lease.Instance)
	delete(m.pendingProcessExits, lease.Instance)
	delete(m.waking, lease.Instance)
	delete(m.live, lease.Instance)
	delete(m.cidToID, GuestVsockCID(lease.Slot))
	delete(m.exportDirs, lease.Instance)
	m.mu.Unlock()
	if !lease.Networkless {
		m.rebuildHostSMTPAllowlistRules(context.WithoutCancel(ctx))
	}
	if m.diskMetrics != nil {
		m.diskMetrics.Delete(lease.Instance)
	}
	return nil
}

func (m *Manager) cleanupOwned(ctx context.Context, retained *instanceCleanup) error {
	lease, nc := retained.lease, retained.net
	// No network teardown or slot release before confirmed child exit. Kill
	// also removes the jail, image mounts, materialised files and cgroup scope.
	if err := m.vmm.Kill(ctx, lease); err != nil {
		return fmt.Errorf("cleanup %s: kill vm: %w", lease.Instance, err)
	}
	if len(retained.workloadNames) > 0 {
		parent := ParentCgroupFor(lease.Plan)
		if lease.IsBuilder {
			parent = BuilderCgroupParent
		}
		removeWorkloadCgroups(filepath.Join(cgroupRoot, parent, PerInstanceScope(lease.Instance)), retained.workloadNames)
	}
	if !lease.Networkless {
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

func (m *Manager) interruptExport(ctx context.Context, instance string) (bool, int32, error) {
	m.mu.Lock()
	exporting := m.exportDirs[instance] != ""
	m.mu.Unlock()
	if !exporting {
		return false, 0, nil
	}
	if interrupter, ok := m.vmm.(interface {
		InterruptBuild(context.Context, string) (int32, error)
	}); ok {
		code, err := interrupter.InterruptBuild(ctx, instance)
		return true, code, err
	}
	return true, 0, fmt.Errorf("vmm: builder interruption unsupported")
}
