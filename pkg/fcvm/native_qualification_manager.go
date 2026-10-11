package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// WithNativeQualificationNodeID binds the attempt-aware surface to the local
// compute identity. Configure it before serving; an unset identity fails closed.
// This does not enable the scheduler consumer or the native lifecycle mode.
func (m *Manager) WithNativeQualificationNodeID(nodeID string) *Manager {
	m.nativeQualificationNodeID = nodeID
	return m
}

func (m *Manager) qualificationJournal(frame state.EnvironmentQualificationExecution) (*nativeQualificationJournal, error) {
	if m.nativeQualificationNodeID == "" || !nativeQualificationUUID(m.nativeQualificationNodeID) {
		return nil, fmt.Errorf("native qualification: local node is not configured: %w", state.ErrConflict)
	}
	if err := validateNativeQualificationFrame(frame, m.nativeQualificationNodeID); err != nil {
		return nil, errors.Join(state.ErrInvalidArgument, err)
	}
	v := m.nativeVMM()
	if v == nil {
		return nil, fmt.Errorf("native qualification: journal-backed lifecycle is unavailable: %w", state.ErrConflict)
	}
	return v.nativeRecoveryRuntime().journal.qualifications(m.nativeQualificationNodeID), nil
}

func (m *Manager) qualificationRestoreJournal(frame state.EnvironmentQualificationExecution) (*nativeQualificationRestoreJournal, error) {
	if m.nativeQualificationNodeID == "" || !nativeQualificationUUID(m.nativeQualificationNodeID) {
		return nil, fmt.Errorf("native qualification restore: local node is not configured: %w", state.ErrConflict)
	}
	if err := validateNativeQualificationRestoreFrame(frame, m.nativeQualificationNodeID); err != nil {
		return nil, errors.Join(state.ErrInvalidArgument, err)
	}
	v := m.nativeVMM()
	if v == nil {
		return nil, fmt.Errorf("native qualification restore: journal-backed lifecycle is unavailable: %w", state.ErrConflict)
	}
	return v.nativeRecoveryRuntime().journal.qualifications(m.nativeQualificationNodeID).restores(), nil
}

func validateNativeQualificationWake(ctx context.Context, frame state.EnvironmentQualificationExecution, req WakeRequest) error {
	fields, ok := wire.FromContext(ctx)
	if !ok || fields.WakeID != frame.WakeID || req.Instance != frame.InstanceID || req.AppID != frame.AppID ||
		req.DeploymentID != frame.DeploymentID || req.MemSizeMiB != frame.RAMMB || !nativeQualificationUUID(req.AccountID) ||
		!req.Plan.Valid() || req.VcpuCount <= 0 || req.CPUMillicores <= 0 || req.BaseKey == "" || frame.Artifact.RootfsKey == "" || req.LayerKey != frame.Artifact.RootfsKey ||
		req.Snapshot != nil || req.KeepPaused || req.ExecutionOnly || req.AppTaskOnly || req.ExportDir != "" || req.BuildTimeoutSec != 0 ||
		req.ExecutionLeaseToken != "" || len(req.ExecutionOutboundIntegrationIDs) != 0 || req.PrivateNetworkID != "" ||
		req.PrivateNetworkAddress != "" || len(req.PrivateNetworkCIDRs) != 0 || len(req.PrivateNetworkAllowedCIDRs) != 0 ||
		len(req.PrivateNetworkFirewallRules) != 0 {
		return fmt.Errorf("native qualification: boot differs from the original workload execution: %w", state.ErrInvalidArgument)
	}
	for _, raw := range req.EgressAllowlist {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.Bits() == 0 {
			return fmt.Errorf("native qualification: invalid egress allowlist: %w", state.ErrInvalidArgument)
		}
	}
	return nil
}

// validateQualificationHostBridge prevents qualification networking from
// silently using an invalid or public bridge address. The netns policy uses
// QualificationOnly to admit only the canonical local proxy and pinned DNS.
func validateQualificationHostBridge(hostBridgeIP netip.Addr) error {
	if !hostBridgeIP.Is4() || !hostBridgeIP.IsPrivate() {
		return fmt.Errorf("native qualification: private IPv4 service bridge is unavailable: %w", state.ErrConflict)
	}
	return nil
}

// isolateQualificationWakeRequest discards app egress exceptions and static
// egress identity for private qualification guests. Scoped service calls still
// pass through the node-local graph proxy, which validates the graph, binding,
// caller and target on every request.
func isolateQualificationWakeRequest(req WakeRequest) (WakeRequest, error) {
	req.EgressAllowlist = nil
	req.EgressPorts = nil
	req.StaticEgressIP = ""
	return req, nil
}

// BootEnvironmentQualificationJob claims an attempt before the ordinary job
// manager is allowed to allocate or create its VM. The private context binds
// every native launch effect to this frame; a normal JobColdBoot request
// cannot borrow a qualification reservation.
func (m *Manager) BootEnvironmentQualificationJob(ctx context.Context, frame state.EnvironmentQualificationExecution, req JobBootRequest) (*Instance, error) {
	j, err := m.qualificationJournal(frame)
	if err != nil {
		return nil, err
	}
	if req.Instance != frame.InstanceID || req.NodeID != frame.NodeID || req.MemSizeMiB != frame.RAMMB ||
		req.ImageRef == "" || len(req.ImageRef) > 2048 || req.ImageRef != frame.Artifact.RootfsKey || frame.Artifact.RootfsKey == "" ||
		req.RunID != frame.RequestID || int64(req.TaskIndex) != frame.Attempt || req.TaskTimeoutSec < 1 ||
		req.TaskTimeoutSec > api.EnvironmentGitOpsJobSmokeMaxTimeoutSeconds || req.VcpuCount != 1 ||
		req.KernelKey == "" || req.BaseKey == "" || !nativeQualificationUUID(req.AccountID) ||
		req.AccountID != strings.TrimSpace(req.AccountID) || !req.StartHeld || !req.Plan.Valid() ||
		!nativeQualificationUUID(req.LeaseToken) || len(req.Command) == 0 {
		return nil, fmt.Errorf("native qualification job: boot differs from the reviewed job contract: %w", state.ErrInvalidArgument)
	}
	if err := (api.EnvironmentJobSmoke{Command: req.Command, TimeoutSeconds: req.TaskTimeoutSec}).Validate(); err != nil {
		return nil, fmt.Errorf("native qualification job: invalid reviewed command: %w", errors.Join(state.ErrInvalidArgument, err))
	}
	if err := validateNativeQualificationWakeIdentity(ctx, frame); err != nil {
		return nil, err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return nil, err
	}
	env, _, waiter, err := m.prepareQualificationJobConfigReceipt(frame.InstanceID, req.Env, req.SealedEnvEntries)
	if err != nil {
		return nil, fmt.Errorf("native qualification job: prepare configuration receipt: %w", err)
	}
	req.Env = env
	defer m.clearQualificationConfigReceipt(frame.InstanceID, waiter)
	record, err := j.claim(ctx, frame)
	if err != nil {
		return nil, err
	}
	inst, err := m.BootJob(nativeQualificationContext(ctx, record), req)
	if err != nil {
		return nil, err
	}
	if err := m.waitForQualificationConfigReceipt(ctx, frame.InstanceID, waiter); err != nil {
		return nil, fmt.Errorf("native qualification job: guest configuration acknowledgement: %w", err)
	}
	return inst, nil
}

func validateNativeQualificationWakeIdentity(ctx context.Context, frame state.EnvironmentQualificationExecution) error {
	fields, ok := wire.FromContext(ctx)
	if !ok || fields.WakeID != frame.WakeID {
		return fmt.Errorf("native qualification job: wake identity differs from the attempt: %w", state.ErrInvalidArgument)
	}
	return nil
}

type nativeQualificationRestoreVMM interface {
	RestoreNativeQualification(context.Context, state.EnvironmentQualificationExecution, Lease, WakeRequest, [2]string, string, string) error
}

// RestoreEnvironmentQualification admits one receipt-backed capture onto a
// distinct, privately tracked target. It never uses generic Restore/Wake,
// which may cold-boot on a miss and publishes through serving indexes.
func (m *Manager) RestoreEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, req WakeRequest) (_ *Instance, err error) {
	j, err := m.qualificationRestoreJournal(frame)
	if err != nil {
		return nil, err
	}
	restorer, ok := m.vmm.(nativeQualificationRestoreVMM)
	if !ok || m.nativeVMM() == nil {
		return nil, fmt.Errorf("native qualification restore: dedicated restore runtime is unavailable: %w", state.ErrConflict)
	}
	req, err = isolateQualificationWakeRequest(req)
	if err != nil {
		return nil, err
	}
	if err := validateNativeQualificationWake(ctx, frame, req); err != nil {
		return nil, err
	}
	// The current restore contract qualifies a single guest workload. A
	// sidecar graph needs per-drive restore evidence and is rejected until that
	// complete contract is available; it must never fall through to generic
	// Restore, which can select a different artifact or lifecycle.
	if len(req.Sidecars) != 0 || len(req.MainDependsOn) != 0 {
		return nil, fmt.Errorf("native qualification restore: workload graph restore is unavailable: %w", state.ErrConflict)
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return nil, err
	}
	target, err := j.claim(ctx, frame, m.fcVersion)
	if err != nil {
		return nil, err
	}
	ctx, deadlineCancel := context.WithDeadline(ctx, target.Deadline)
	defer deadlineCancel()
	ctx = nativeQualificationRestoreContext(ctx, target)
	bootCtx, flight, err := m.beginInstanceBoot(ctx, frame.InstanceID)
	if err != nil {
		return nil, err
	}
	defer m.finishInstanceFlight(frame.InstanceID, flight)
	if err := bootCtx.Err(); err != nil {
		return nil, err
	}
	lease, err := m.alloc.Acquire(frame.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("native qualification restore: acquire private target: %w", err)
	}
	lease.Plan = req.Plan
	lease.MemoryMaxMiB = req.MemSizeMiB
	lease.CPUMillicores = req.CPUMillicores
	lease.DisableStartupCPUBoost = req.DisableStartupCPUBoost
	lease = m.beginProcessAttempt(lease)
	var nc netns.Config
	nativeLeasePrepared := false
	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if nativeLeasePrepared {
			err = errors.Join(err, m.cleanup(cleanupCtx, lease, nc, nil))
			return
		}
		// Native prepare has not returned success, so release only when its
		// durable launch record proves that no physical owner was created.
		if v := m.nativeVMM(); v != nil {
			_, readErr := v.nativeRecoveryRuntime().journal.read(lease.Instance)
			if errors.Is(readErr, os.ErrNotExist) {
				err = errors.Join(err, m.alloc.Release(lease.Instance))
				m.mu.Lock()
				delete(m.processGenerations, lease.Instance)
				delete(m.pendingProcessExits, lease.Instance)
				m.mu.Unlock()
				return
			}
		}
		err = errors.Join(err, m.cleanup(cleanupCtx, lease, nc, nil))
	}()
	nc, err = qualificationNetworkConfig(lease, req, m.conntrackCap, m.dnsGatingOff)
	if err != nil {
		return nil, err
	}
	if err := m.prepareNativeLease(bootCtx, lease); err != nil {
		return nil, fmt.Errorf("native qualification restore: prepare target ownership: %w", err)
	}
	nativeLeasePrepared = true
	if err := m.journalLease(lease); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.waking == nil {
		m.waking = make(map[string]struct{})
	}
	m.waking[frame.InstanceID] = struct{}{}
	m.mu.Unlock()
	if err := m.setupNetwork(bootCtx, nc); err != nil {
		return nil, fmt.Errorf("native qualification restore: private network setup: %w", err)
	}
	if err := bootCtx.Err(); err != nil {
		return nil, err
	}
	kernelPath, err := m.resolveBackingFile(m.paths.Kernel)
	if err != nil {
		return nil, fmt.Errorf("native qualification restore: resolve original kernel candidate: %w", err)
	}
	basePath, err := m.resolveBackingFile(req.BaseKey)
	if err != nil {
		return nil, fmt.Errorf("native qualification restore: resolve original base candidate: %w", err)
	}
	var waiter *qualificationConfigReceiptWaiter
	req, waiter, err = m.prepareQualificationConfigReceipt(req)
	if err != nil {
		return nil, fmt.Errorf("native qualification restore: prepare config receipt: %w", err)
	}
	defer m.clearQualificationConfigReceipt(frame.InstanceID, waiter)
	req.preparedSecretsEnvJSON, req.preparedAPIEnvJSON, err = m.prepareWakeFiles(req)
	if err != nil {
		return nil, fmt.Errorf("native qualification restore: prepare frozen runtime files: %w", err)
	}
	serviceDiscoveryIP := ""
	if nc.HostBridgeIP.IsValid() {
		serviceDiscoveryIP = nc.HostBridgeIP.String()
	}
	if err := restorer.RestoreNativeQualification(bootCtx, frame, lease, req, [2]string{kernelPath, basePath}, m.fcVersion, serviceDiscoveryIP); err != nil {
		return nil, fmt.Errorf("native qualification restore: dedicated native restore: %w", err)
	}
	if err := m.waitForQualificationConfigReceipt(bootCtx, frame.InstanceID, waiter); err != nil {
		return nil, err
	}
	inst := &Instance{Lease: lease, Net: nc, Method: WakeRestore, QualificationOnly: true, AppID: req.AppID, AccountID: req.AccountID,
		DeploymentID: req.DeploymentID, Plan: req.Plan, Port: req.Port, HealthcheckPath: req.HealthcheckPath,
		StartupDeadlineS: req.StartupDeadlineS}
	if err := m.stampNativeInstanceGeneration(inst); err != nil {
		return nil, fmt.Errorf("native qualification restore: target process identity: %w", err)
	}
	m.mu.Lock()
	if flight.cancelled || bootCtx.Err() != nil || m.instanceStops[frame.InstanceID] != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("native qualification restore: canceled before private ownership publication: %w", context.Canceled)
	}
	if exitCode, exited := m.pendingProcessExits[frame.InstanceID]; exited {
		delete(m.pendingProcessExits, frame.InstanceID)
		delete(m.waking, frame.InstanceID)
		m.mu.Unlock()
		return nil, fmt.Errorf("native qualification restore: target exited before ownership publication (code %d): %w", exitCode, state.ErrConflict)
	}
	delete(m.waking, frame.InstanceID)
	m.qualificationInstances[frame.InstanceID] = inst
	m.mu.Unlock()
	return inst, nil
}

func qualificationNetworkConfig(lease Lease, req WakeRequest, conntrackCap int64, dnsGatingOff bool) (netns.Config, error) {
	nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)
	nc.TapUID = lease.UID
	nc.EgressMbit = req.EgressMbit
	if nc.EgressMbit == 0 {
		if limits, ok := api.LimitsFor(req.Plan); ok {
			nc.EgressMbit = limits.EgressMbit
		}
	}
	nc.GuestAppPort = req.Port
	nc.ConntrackCap = conntrackCap
	applyTenantEgressPolicy(&nc, req.Plan, nil)
	nc.QualificationOnly = true
	if err := validateQualificationHostBridge(nc.HostBridgeIP); err != nil {
		return netns.Config{}, err
	}
	nc.DNSGated = nc.DNSGated && !dnsGatingOff
	return nc, nil
}

func isolateQualificationJobNetwork(nc netns.Config) netns.Config {
	nc.EgressAllowlist = nil
	nc.EgressPorts = nil
	nc.OperatorExceptions = nil
	nc.PrivateNetworkCIDRs = nil
	nc.PrivateNetworkAllowedCIDRs = nil
	nc.PrivateNetworkFirewallRules = nil
	nc.QualificationOnly = true
	return nc
}

// WakeEnvironmentQualification claims one original incoming attempt before
// any allocation or native effect. Duplicate delivery cannot create another
// physical generation. Schedd supplies the frozen workload payload; vmmd
// verifies its placement, artifact and guest reservation at this boundary.
func (m *Manager) WakeEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, req WakeRequest) (*Instance, error) {
	j, err := m.qualificationJournal(frame)
	if err != nil {
		return nil, err
	}
	req, err = isolateQualificationWakeRequest(req)
	if err != nil {
		return nil, err
	}
	if err := validateNativeQualificationWake(ctx, frame, req); err != nil {
		return nil, err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return nil, err
	}
	// Prepare all sidecar projections before the private VM starts so the
	// receipt waiter can bind every workload's actual boot environment. Wake's
	// own preparation step is idempotent for these already-prepared entries.
	if err := m.prepareSidecarEnvFiles(&req); err != nil {
		return nil, fmt.Errorf("native qualification: prepare sidecar configuration: %w", err)
	}
	req, receiptWaiter, err := m.prepareQualificationConfigReceipt(req)
	if err != nil {
		return nil, fmt.Errorf("native qualification: prepare guest configuration receipt: %w", err)
	}
	defer m.clearQualificationConfigReceipt(frame.InstanceID, receiptWaiter)
	record, err := j.claim(ctx, frame)
	if err != nil {
		return nil, err
	}
	inst, err := m.Wake(nativeQualificationContext(ctx, record), req)
	if err != nil {
		return nil, err
	}
	if err := m.waitForQualificationConfigReceipt(ctx, frame.InstanceID, receiptWaiter); err != nil {
		return nil, err
	}
	return inst, nil
}

// RetireEnvironmentQualification revokes delayed creation, joins Manager's
// original boot/teardown and verifies durable physical retirement. A request
// tombstone or generic destroy result cannot supply a receipt. When the
// original attempt never published a physical owner, the journal and joined
// Manager state can instead prove that no native effects exist.
func (m *Manager) RetireEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (state.EnvironmentQualificationRetirement, error) {
	var j *nativeQualificationJournal
	var restore *nativeQualificationRestoreJournal
	var err error
	if frame.CaptureInstanceID != "" {
		restore, err = m.qualificationRestoreJournal(frame)
	} else {
		j, err = m.qualificationJournal(frame)
	}
	if err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	if restore != nil {
		record, err := restore.revoke(ctx, frame)
		if err != nil {
			return state.EnvironmentQualificationRetirement{}, err
		}
		if proof, proofErr := m.retireUnownedQualificationTarget(ctx, frame.InstanceID, func() (state.EnvironmentQualificationRetirement, error) {
			return restore.noNativeEffectsRetirement(ctx, frame)
		}); proofErr == nil {
			return proof, nil
		} else if record.NativeGeneration == "" {
			return state.EnvironmentQualificationRetirement{}, proofErr
		}
		if record.NativeGeneration == "" {
			return state.EnvironmentQualificationRetirement{}, fmt.Errorf("native qualification restore: no bound physical retirement evidence: %w", state.ErrConflict)
		}
		if err := m.Destroy(ctx, frame.InstanceID); err != nil {
			return state.EnvironmentQualificationRetirement{}, err
		}
		return restore.retirement(ctx, frame)
	}
	record, err := j.revoke(ctx, frame)
	if err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	if proof, proofErr := m.retireUnownedQualificationTarget(ctx, frame.InstanceID, func() (state.EnvironmentQualificationRetirement, error) {
		return j.noNativeEffectsRetirement(ctx, frame)
	}); proofErr == nil {
		return proof, nil
	} else if record.NativeGeneration == "" {
		return state.EnvironmentQualificationRetirement{}, proofErr
	}
	if record.NativeGeneration == "" {
		return state.EnvironmentQualificationRetirement{}, fmt.Errorf("native qualification: no bound physical retirement evidence: %w", state.ErrConflict)
	}
	if err := m.Destroy(ctx, frame.InstanceID); err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	return j.retirement(ctx, frame)
}

// retireUnownedQualificationTarget joins the private boot and releases its
// allocator identity only after the incoming journal is revoked and its
// physical launch journal is absent. This closes failures before the first
// native owner is published without treating a missing parent in an otherwise
// bound lifecycle as successful retirement.
func (m *Manager) retireUnownedQualificationTarget(ctx context.Context, instance string, verify func() (state.EnvironmentQualificationRetirement, error)) (state.EnvironmentQualificationRetirement, error) {
	stop, err := m.beginInstanceStop(ctx, instance)
	if err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	defer m.finishInstanceStop(instance, stop)
	if err := m.cancelInFlightInstance(ctx, instance); err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	proof, err := verify()
	if err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, quarantined := m.restartQuarantine[instance]
	if m.instanceFlights[instance] != nil || m.live[instance] != nil || m.qualificationInstances[instance] != nil ||
		quarantined || m.exportDirs[instance] != "" || m.preparedNetworks != nil {
		return state.EnvironmentQualificationRetirement{}, state.ErrConflict
	}
	if retained := m.pendingCleanup[instance]; retained != nil && retained.lease.Instance != instance {
		return state.EnvironmentQualificationRetirement{}, state.ErrConflict
	}
	if len(m.nativeRecovered[instance]) != 0 {
		return state.EnvironmentQualificationRetirement{}, state.ErrConflict
	}
	m.alloc.mu.Lock()
	defer m.alloc.mu.Unlock()
	for _, owner := range m.alloc.recovered {
		if owner == instance {
			return state.EnvironmentQualificationRetirement{}, state.ErrConflict
		}
	}
	if _, reserved := m.alloc.reserved[instance]; reserved {
		return state.EnvironmentQualificationRetirement{}, state.ErrConflict
	}
	if slot, leased := m.alloc.byInstance[instance]; leased {
		if retained := m.pendingCleanup[instance]; retained != nil && retained.lease.Slot != slot {
			return state.EnvironmentQualificationRetirement{}, state.ErrConflict
		}
		delete(m.alloc.byInstance, instance)
		m.alloc.free = append(m.alloc.free, slot)
		delete(m.cidToID, GuestVsockCID(slot))
	} else if m.pendingCleanup[instance] != nil {
		return state.EnvironmentQualificationRetirement{}, state.ErrConflict
	}
	delete(m.pendingCleanup, instance)
	delete(m.pendingProcessExits, instance)
	delete(m.processGenerations, instance)
	delete(m.waking, instance)
	return proof, nil
}

// Incoming generation is the stable receipt identity only after the original
// physical record proves exit and complete resource removal. No extra receipt
// is minted on retries; the original incoming and physical UUIDs stay distinct.
func (j *nativeQualificationJournal) retirement(ctx context.Context, frame state.EnvironmentQualificationExecution) (proof state.EnvironmentQualificationRetirement, result error) {
	lock, err := j.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	record, err := j.read(frame.InstanceID)
	if err != nil {
		return proof, err
	}
	if record.Execution != frame || !record.Revoked || record.NativeGeneration == "" {
		return proof, fmt.Errorf("native qualification: original attempt has no retirement authority: %w", state.ErrConflict)
	}
	physicalLock, err := j.owner.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, physicalLock.Close()) }()
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil {
		return proof, err
	}
	if physical.Generation != record.NativeGeneration || physical.KernelBootID != record.KernelBootID ||
		!sameNativePhysicalLease(physical.Lease, record.NativeLease) || !physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
		return proof, fmt.Errorf("native qualification: original physical retirement is unconfirmed: %w", state.ErrConflict)
	}
	return state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: record.Generation,
		NativeGeneration: physical.Generation, KernelBootID: physical.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}, nil
}
