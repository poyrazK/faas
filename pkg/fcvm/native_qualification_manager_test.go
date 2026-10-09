//go:build linux || darwin

// adr: 568 — qualification admission and retirement retain the original native owner.
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func nativeQualificationManagerFixture(t *testing.T) (*Manager, *JailerVMM, state.EnvironmentQualificationExecution, WakeRequest, context.Context) {
	t.Helper()
	m, v, _ := nativeManagerFixture(t)
	_, frame, ctx := nativeQualificationFixture(t)
	m.WithNativeQualificationNodeID(frame.NodeID)
	ctx = wire.WithContext(ctx, wire.CorrelationFields{WakeID: frame.WakeID})
	req := WakeRequest{Instance: frame.InstanceID, AppID: frame.AppID, DeploymentID: frame.DeploymentID,
		AccountID: uuid.NewString(), Plan: api.PlanHobby, MemSizeMiB: frame.RAMMB, VcpuCount: 2, CPUMillicores: 1000,
		BaseKey: "base/runtime.ext4", LayerKey: frame.Artifact.RootfsKey}
	return m, v, frame, req, ctx
}

func TestQualificationWakePolicyDropsTenantEgressInputs(t *testing.T) {
	req := WakeRequest{Plan: api.PlanHobby}
	req.EgressAllowlist = []string{"8.8.8.0/24"}
	req.EgressPorts = []uint16{5432}
	req.StaticEgressIP = "203.0.113.9"
	isolated, err := isolateQualificationWakeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(isolated.EgressAllowlist) != 0 || len(isolated.EgressPorts) != 0 || isolated.StaticEgressIP != "" {
		t.Fatalf("tenant egress settings survived qualification isolation: %+v", isolated)
	}

	lease := Lease{Instance: "qualification-1", Netns: "fc-qualification", VethHost: "vhq", VethPeer: "vpq", HostIP: netip.MustParseAddr("10.100.0.9")}
	nc, err := qualificationNetworkConfig(lease, req, api.DefaultConntrackCap, false)
	if err != nil {
		t.Fatal(err)
	}
	if !nc.QualificationOnly || len(nc.EgressAllowlist) != 0 {
		t.Fatalf("qualification network was not isolated: qualification_only=%v allowlist=%v", nc.QualificationOnly, nc.EgressAllowlist)
	}
	for _, command := range nc.NftCommands() {
		line := strings.Join(command, " ")
		if strings.Contains(line, "tcp dport != @egress_ports") || strings.Contains(line, "ip daddr { 8.8.8.0/24 }") {
			t.Fatalf("qualification network admitted tenant egress policy: %s", line)
		}
	}
}

func TestQualificationJobNetworkDropsTenantEgress(t *testing.T) {
	nc := netns.Config{
		EgressAllowlist: []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")},
		EgressPorts:     []uint16{5432}, OperatorExceptions: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")},
		PrivateNetworkCIDRs:         []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")},
		PrivateNetworkAllowedCIDRs:  []netip.Prefix{netip.MustParsePrefix("192.168.1.8/32")},
		PrivateNetworkFirewallRules: []netns.PrivateNetworkFirewallRule{{Protocol: "tcp", Ports: []netns.PrivateNetworkFirewallPortRange{{Start: 443, End: 443}}}},
	}
	nc = isolateQualificationJobNetwork(nc)
	if !nc.QualificationOnly || len(nc.EgressAllowlist) != 0 || len(nc.EgressPorts) != 0 || len(nc.OperatorExceptions) != 0 ||
		len(nc.PrivateNetworkCIDRs) != 0 || len(nc.PrivateNetworkAllowedCIDRs) != 0 || len(nc.PrivateNetworkFirewallRules) != 0 {
		t.Fatalf("job qualification inherited tenant network policy: %+v", nc)
	}
}

func TestNativeQualificationJobRejectsChangedBootBeforeClaim(t *testing.T) {
	for _, change := range []string{"node", "instance", "image", "memory", "run", "attempt", "timeout", "vcpu", "command", "account", "lease", "kernel", "base", "env", "wake"} {
		t.Run(change, func(t *testing.T) {
			m, v, frame, _, ctx := nativeQualificationManagerFixture(t)
			req := JobBootRequest{Instance: frame.InstanceID, AccountID: uuid.NewString(), NodeID: frame.NodeID, Plan: api.PlanHobby,
				RunID: frame.RequestID, TaskIndex: int(frame.Attempt), ImageRef: frame.Artifact.RootfsKey, KernelKey: "kernel/fc", BaseKey: "base/runtime",
				Command: []string{"node", "smoke.js"}, VcpuCount: 1, MemSizeMiB: frame.RAMMB, TaskTimeoutSec: 30, LeaseToken: uuid.NewString()}
			switch change {
			case "node":
				req.NodeID = uuid.NewString()
			case "instance":
				req.Instance = uuid.NewString()
			case "image":
				req.ImageRef = "other/image"
			case "memory":
				req.MemSizeMiB++
			case "run":
				req.RunID = uuid.NewString()
			case "attempt":
				req.TaskIndex++
			case "timeout":
				req.TaskTimeoutSec = api.EnvironmentGitOpsJobSmokeMaxTimeoutSeconds + 1
			case "vcpu":
				req.VcpuCount = 2
			case "command":
				req.Command = []string{" "}
			case "account":
				req.AccountID = " "
			case "lease":
				req.LeaseToken = "invalid"
			case "kernel":
				req.KernelKey = ""
			case "base":
				req.BaseKey = ""
			case "env":
				req.Env = map[string]string{"UNREVIEWED": "value"}
			case "wake":
				ctx = wire.WithContext(ctx, wire.CorrelationFields{WakeID: uuid.NewString()})
			}
			if _, err := m.BootEnvironmentQualificationJob(ctx, frame, req); err == nil {
				t.Fatal("changed job request gained qualification boot authority")
			}
			journal := v.nativeRecovery.journal.qualifications(frame.NodeID)
			if _, err := journal.read(frame.InstanceID); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected job request claimed native qualification authority", err)
			}
			if m.alloc.InUse() != 0 || m.LiveCount() != 0 || len(m.run.(*fakeRunner).commands) != 0 {
				t.Fatal("rejected job request reached allocation or native effects")
			}
		})
	}
}

func TestQualificationRequiresPrivateIPv4ServiceBridge(t *testing.T) {
	for _, raw := range []string{"203.0.113.1", "2001:db8::1", "0.0.0.0"} {
		t.Run(raw, func(t *testing.T) {
			if err := validateQualificationHostBridge(netip.MustParseAddr(raw)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("qualification accepted non-private service bridge %s: %v", raw, err)
			}
		})
	}
}

func TestLiveNetworkReconcilesSkipQualificationInstances(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	appID := uuid.NewString()
	instanceID := uuid.NewString()
	m.mu.Lock()
	m.live[instanceID] = &Instance{AppID: appID, QualificationOnly: true, Net: netns.Config{Instance: instanceID, Netns: "fc-qualification"}}
	m.mu.Unlock()

	if err := m.UpdateEgressAllowlist(context.Background(), appID, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateEgressPorts(context.Background(), appID, []uint16{5432}); err != nil {
		t.Fatal(err)
	}
	cidrs := []netip.Prefix{netip.MustParsePrefix("192.168.201.0/24")}
	if err := m.UpdatePrivateNetwork(context.Background(), appID, cidrs); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdatePrivateNetworkAttachment(context.Background(), appID, "qnet", netip.MustParseAddr("192.168.201.5"), cidrs); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.SnapshotLiveEgress()[instanceID]; ok {
		t.Fatal("qualification target appeared in the ordinary egress poller snapshot")
	}

	m.mu.Lock()
	inst := m.live[instanceID]
	if inst == nil || len(inst.Net.EgressAllowlist) != 0 || len(inst.Net.EgressPorts) != 0 || len(inst.Net.PrivateNetworkCIDRs) != 0 || inst.Net.PrivateVethHost != "" {
		m.mu.Unlock()
		t.Fatalf("reconcile mutated qualification network state: %+v", inst)
	}
	m.mu.Unlock()
	run.mu.Lock()
	commands := len(run.commands)
	run.mu.Unlock()
	if commands != 0 {
		t.Fatalf("live app policy reconcile reached qualification network runner %d times", commands)
	}
}

func TestNativeQualificationManagerRejectsChangedBootBeforeAuthority(t *testing.T) {
	for _, change := range []string{"unconfigured", "restore", "node", "instance", "app", "deployment", "memory", "artifact", "path_only", "account", "plan", "snapshot", "paused", "execution", "app_task", "builder", "wake"} {
		t.Run(change, func(t *testing.T) {
			m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
			switch change {
			case "restore":
				frame.CaptureInstanceID = uuid.NewString()
			case "unconfigured":
				m.WithNativeQualificationNodeID("")
			case "node":
				frame.NodeID = uuid.NewString()
			case "instance":
				req.Instance = uuid.NewString()
			case "app":
				req.AppID = uuid.NewString()
			case "deployment":
				req.DeploymentID = uuid.NewString()
			case "memory":
				req.MemSizeMiB++
			case "artifact":
				req.LayerKey = "apps/replacement.ext4"
			case "path_only":
				frame.Artifact.RootfsPath, frame.Artifact.RootfsKey = "/legacy/qualified.ext4", ""
			case "account":
				req.AccountID = ""
			case "plan":
				req.Plan = "unknown"
			case "snapshot":
				req.Snapshot = &Snapshot{}
			case "paused":
				req.KeepPaused = true
			case "execution":
				req.ExecutionOnly = true
			case "app_task":
				req.AppTaskOnly = true
			case "builder":
				req.ExportDir = t.TempDir()
			case "wake":
				ctx = wire.WithContext(ctx, wire.CorrelationFields{WakeID: uuid.NewString()})
			}
			if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
				t.Fatal("changed request gained boot authority")
			}
			j := v.nativeRecovery.journal.qualifications(frame.NodeID)
			path, err := j.path(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected boot published incoming authority", err)
			}
			if m.alloc.InUse() != 0 || m.LiveCount() != 0 || len(m.run.(*fakeRunner).commands) != 0 {
				t.Fatal("rejected boot reached allocation or native effects")
			}
		})
	}
}

func TestNativeQualificationManagerRetirementRequiresCompleteOriginalProof(t *testing.T) {
	m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
	j := v.nativeRecovery.journal.qualifications(frame.NodeID)
	m.run = &nativeOwnershipRunner{journal: j.owner, instance: frame.InstanceID}
	cause := errors.New("original resources are still present")
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return cause }
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); !errors.Is(err, cause) {
		t.Fatalf("failed cleanup was lost: %v", err)
	}
	bound, err := j.read(frame.InstanceID)
	if err != nil || bound.NativeGeneration == "" || bound.NativeLease.Instance != frame.InstanceID || m.alloc.InUse() != 1 {
		t.Fatal("original native reservation was not retained", err)
	}
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
		t.Fatal("duplicate delivery created a replacement")
	}
	proof, err := m.RetireEnvironmentQualification(ctx, frame)
	if !errors.Is(err, cause) || proof != (state.EnvironmentQualificationRetirement{}) || m.alloc.InUse() != 1 {
		t.Fatalf("incomplete removal supplied retirement or released the lease: %+v %v", proof, err)
	}
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return nil }
	proof, err = m.RetireEnvironmentQualification(ctx, frame)
	if err != nil || proof.Kind != state.QualificationNativeRetired || proof.ReceiptID != bound.Generation ||
		proof.NativeGeneration != bound.NativeGeneration || proof.KernelBootID != bound.KernelBootID || !proof.ProcessesExited || !proof.ResourcesRemoved ||
		m.alloc.InUse() != 0 || m.LiveCount() != 0 || len(m.cidToID) != 0 {
		t.Fatalf("original retirement was not acknowledged: %+v %v", proof, err)
	}
	again, err := m.RetireEnvironmentQualification(ctx, frame)
	if err != nil || again != proof {
		t.Fatalf("retry minted a different native receipt: %+v %v", again, err)
	}
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
		t.Fatal("late delivery recreated a retired attempt")
	}
}

func TestNativeQualificationManagerRestoresPrivatelyAndRetiresFromRestoreJournal(t *testing.T) {
	m, v, source, sourceReq, ctx := nativeQualificationManagerFixture(t)
	q := v.nativeRecovery.journal.qualifications(source.NodeID)
	incoming, err := q.claim(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.owner.prepare(nativeQualificationContext(ctx, incoming), qualificationLease(source.InstanceID)); err != nil {
		t.Fatal(err)
	}
	incoming, err = q.read(source.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	capture := nativeQualificationCaptureRecord{Version: 1, InstanceID: source.InstanceID, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, StartedAt: incoming.AcceptedAt.Add(time.Millisecond),
		CompletedAt: incoming.AcceptedAt.Add(2 * time.Millisecond), FCVersion: "1.7.0", Info: SnapshotInfo{MemBytes: 100, VMStateBytes: 50, StoredBytes: 200},
		Backing: BackingIdentity{Version: 1, Kernel: "sha256:modeled-kernel", Base: "sha256:modeled-base"}}
	writeCompleteNativeQualificationCapture(t, q, incoming, capture)
	if _, err := q.revoke(ctx, source); err != nil {
		t.Fatal(err)
	}
	sourceOwner, err := q.owner.read(source.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	sourceOwner.ExitConfirmed, sourceOwner.ResourcesRemoved = true, true
	if err := q.owner.write(sourceOwner); err != nil {
		t.Fatal(err)
	}

	target := source
	target.InstanceID, target.WakeID, target.CleanupToken, target.CaptureInstanceID = uuid.NewString(), uuid.NewString(), uuid.NewString(), source.InstanceID
	restores := q.restores()
	deadline := time.Now().Add(time.Minute)
	targetCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	targetCtx = wire.WithContext(targetCtx, wire.CorrelationFields{WakeID: target.WakeID})
	kernelPath := filepath.Join(t.TempDir(), "vmlinux")
	basePath := filepath.Join(t.TempDir(), "runtime.ext4")
	if err := os.WriteFile(kernelPath, []byte("kernel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.paths.Kernel = kernelPath
	var fullSlots []string
	for i := 0; i < MaxSlots; i++ {
		id := uuid.NewString()
		if _, err := m.alloc.Acquire(id); err != nil {
			t.Fatal(err)
		}
		fullSlots = append(fullSlots, id)
	}
	failedTarget := source
	failedTarget.InstanceID, failedTarget.WakeID, failedTarget.CleanupToken, failedTarget.CaptureInstanceID = uuid.NewString(), uuid.NewString(), uuid.NewString(), source.InstanceID
	failedCtx, failedCancel := context.WithDeadline(ctx, time.Now().Add(time.Minute))
	failedCtx = wire.WithContext(failedCtx, wire.CorrelationFields{WakeID: failedTarget.WakeID})
	failedReq := sourceReq
	failedReq.Instance, failedReq.BaseKey = failedTarget.InstanceID, basePath
	if _, err := m.RestoreEnvironmentQualification(failedCtx, failedTarget, failedReq); err == nil {
		failedCancel()
		t.Fatal("full node admitted a qualification restore without a lease")
	}
	failedProof, err := m.RetireEnvironmentQualification(failedCtx, failedTarget)
	failedCancel()
	if err != nil || failedProof.Kind != state.QualificationNativeEffectsAbsent || failedProof.ReceiptID == "" ||
		failedProof.NativeGeneration != "" || !failedProof.ResourcesRemoved || m.alloc.InUse() != MaxSlots {
		t.Fatalf("failed pre-owner restore was not retired safely: %+v %v", failedProof, err)
	}
	for _, id := range fullSlots {
		if err := m.alloc.Release(id); err != nil {
			t.Fatal(err)
		}
	}
	restoreVMM := m.vmm.(*recoveryVMMFixture)
	restoreVMM.restoreQualification = func(_ context.Context, got state.EnvironmentQualificationExecution, lease Lease, req WakeRequest, candidates [2]string, fcVersion, _ string) error {
		if got != target || lease.Instance != target.InstanceID || req.Instance != target.InstanceID ||
			candidates != [2]string{kernelPath, basePath} || fcVersion != "1.7.0" {
			return errors.New("fixture: restore did not receive the pinned private execution")
		}
		m.mu.Lock()
		waiter := m.qualificationConfigReceiptWaiters[target.InstanceID]
		if waiter == nil {
			m.mu.Unlock()
			return errors.New("fixture: config receipt waiter was not registered")
		}
		expected := waiter.expected[WorkloadNameMain]
		token := waiter.token
		m.mu.Unlock()
		return m.MarkEnvironmentQualificationConfigApplied(target.InstanceID, EnvironmentQualificationConfigReceipt{
			Token: token, Workload: WorkloadNameMain, APIEnvSHA256: expected.apiEnvSHA256,
			SecretsFileRead: expected.secretsRead, SecretKeysMAC: expected.secretKeysMAC, ConfigMAC: expected.configMAC,
		})
	}
	req := sourceReq
	req.Instance, req.BaseKey = target.InstanceID, basePath
	inst, err := m.RestoreEnvironmentQualification(targetCtx, target, req)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := restores.read(target.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	targetBound, err := restores.read(target.InstanceID)
	if err != nil || targetBound.NativeGeneration == "" || targetBound.NativeGeneration == incoming.NativeGeneration {
		t.Fatal("restore target did not receive a distinct native owner", err)
	}
	if inst == nil || inst.Method != WakeRestore || inst.Lease.Instance != target.InstanceID || m.LiveCount() != 0 || len(m.cidToID) != 0 ||
		m.qualificationInstances[target.InstanceID] != inst || m.live[target.InstanceID] != nil || m.HasInstanceOwnership(target.InstanceID) == false {
		t.Fatal("qualification restore escaped its private lifecycle registry")
	}

	proof, err := m.RetireEnvironmentQualification(ctx, target)
	if err != nil || proof.Kind != state.QualificationNativeRetired || proof.ReceiptID != accepted.Generation ||
		proof.NativeGeneration != targetBound.NativeGeneration || proof.NativeGeneration == incoming.NativeGeneration || !proof.ProcessesExited || !proof.ResourcesRemoved {
		t.Fatalf("restore target did not retire under its own journal: %+v %v", proof, err)
	}
	if m.LiveCount() != 0 || len(m.cidToID) != 0 || m.qualificationInstances[target.InstanceID] != nil {
		t.Fatal("private restore retirement leaked its lifecycle identity")
	}
}

func TestNativeQualificationManagerRetireBeforeCreateProvesNoNativeEffects(t *testing.T) {
	m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
	proof, err := m.RetireEnvironmentQualification(ctx, frame)
	if err != nil || proof.Kind != state.QualificationNativeEffectsAbsent || proof.ReceiptID == "" || proof.NativeGeneration != "" ||
		proof.KernelBootID == "" || !proof.ProcessesExited || !proof.ResourcesRemoved {
		t.Fatalf("physical absence was not attested: %+v %v", proof, err)
	}
	record, err := v.nativeRecovery.journal.qualifications(frame.NodeID).read(frame.InstanceID)
	if err != nil || !record.Revoked || record.CreateStarted || record.NativeGeneration != "" {
		t.Fatal("original revocation was not durable", err)
	}
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
		t.Fatal("delayed delivery passed the original revocation")
	}
	if m.alloc.InUse() != 0 || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("delayed delivery reached native effects")
	}
}

func TestNativeQualificationManagerCannotRetireForeignPhysicalOwner(t *testing.T) {
	m, v, frame, _, ctx := nativeQualificationManagerFixture(t)
	lease := qualificationLease(frame.InstanceID)
	if err := v.prepareNativeLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	before, err := v.nativeRecovery.journal.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := m.RetireEnvironmentQualification(ctx, frame)
	if !errors.Is(err, state.ErrConflict) || proof != (state.EnvironmentQualificationRetirement{}) {
		t.Fatal("unbound incoming request borrowed foreign native authority", err)
	}
	after, err := v.nativeRecovery.journal.read(frame.InstanceID)
	if err != nil || after != before || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("retirement touched a foreign physical owner", err)
	}
}

func TestNativeQualificationManagerMissingPhysicalPublicationUsesNoEffectsProof(t *testing.T) {
	m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
	cause := errors.New("physical publication unavailable")
	v.nativeRecovery.journal.writeRecord = func(string, nativeLaunchRecord) error { return cause }
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); !errors.Is(err, cause) {
		t.Fatal("physical publication uncertainty was ignored", err)
	}
	v.nativeRecovery.journal.writeRecord = nil
	proof, err := m.RetireEnvironmentQualification(ctx, frame)
	if err != nil || proof.Kind != state.QualificationNativeEffectsAbsent || proof.ReceiptID == "" || proof.NativeGeneration != "" ||
		!proof.ProcessesExited || !proof.ResourcesRemoved || m.alloc.InUse() != 0 || m.pendingCleanup[frame.InstanceID] != nil {
		t.Fatalf("missing physical parent was not safely excluded: %+v %v", proof, err)
	}
}

func TestNativeQualificationRetirementCannotBorrowChangedFrameOrPhysicalIdentity(t *testing.T) {
	for _, change := range []string{"frame", "generation", "lease", "unremoved"} {
		t.Run(change, func(t *testing.T) {
			m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
			j := v.nativeRecovery.journal.qualifications(frame.NodeID)
			m.run = &nativeOwnershipRunner{journal: j.owner, instance: frame.InstanceID}
			if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
				t.Fatal("fixture should fail network setup")
			}
			if _, err := j.revoke(ctx, frame); err != nil {
				t.Fatal(err)
			}
			physical, err := j.owner.read(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "frame":
				frame.CleanupToken = uuid.NewString()
			case "generation":
				physical.Generation = uuid.NewString()
			case "lease":
				physical.Lease.CPUMillicores++
			case "unremoved":
				physical.ResourcesRemoved = false
			}
			if err := j.owner.write(physical); err != nil {
				t.Fatal(err)
			}
			if proof, err := j.retirement(ctx, frame); err == nil || proof != (state.EnvironmentQualificationRetirement{}) {
				t.Fatal("changed identity supplied the original receipt", err)
			}
		})
	}
}
