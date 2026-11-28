package vmmdgrpc

// adr: 20261122132055 §3 — a fork captures a running session VM without
// ending it: the guest pauses only while the capture is written and its
// volume cloned, then runs on. The capture and the volume clone upload in
// the background, as a pause's do; each fork restores them into a session of
// its own.

import (
	"context"
	"time"

	"github.com/google/uuid"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// sandboxForker is the VMM surface a fork needs; *fcvm.Manager has it.
type sandboxForker interface {
	ForkSandbox(ctx context.Context, f fcvm.SandboxFork) (fcvm.SandboxForked, error)
}

// forkSandbox captures the session VM and answers forked, or fork_refused;
// the VM keeps running either way.
func (s *Server) forkSandbox(ctx context.Context, instance string, act *sandboxActivity, f *vmmdpb.RunSandboxFork) *vmmdpb.RunSandboxEvent {
	refuse := func(reason string) *vmmdpb.RunSandboxEvent {
		return &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_ForkRefused{ForkRefused: &vmmdpb.RunSandboxForkRefused{Reason: reason}}}
	}
	forker, ok := s.vmm.(sandboxForker)
	if !ok {
		return refuse("unsupported")
	}
	if id, err := uuid.Parse(f.GetCaptureId()); err != nil || id.String() != f.GetCaptureId() {
		return refuse("invalid")
	}
	act.mu.Lock()
	pausing := act.pausing
	act.mu.Unlock()
	if pausing || !act.forking.CompareAndSwap(false, true) {
		return refuse("busy")
	}
	defer act.forking.Store(false)
	if err := s.thawSandbox(ctx, instance, act); err != nil {
		s.log.Warn("sandbox: thaw before a fork failed", "instance", instance, "err", err)
		return refuse("failed")
	}
	spec := fcvm.SandboxFork{Instance: instance, SandboxID: act.sandboxID, AccountID: act.accountID, CaptureID: f.GetCaptureId()}
	if act.volume && s.sandboxVolumes != nil && s.sandboxStorage != nil {
		spec.Volumes, spec.VolumeBackupID = s.sandboxVolumes, uuid.NewString()
	}
	started := time.Now()
	forked, err := forker.ForkSandbox(ctx, spec)
	s.ops.Observe("SandboxFork", time.Since(started), err)
	if err != nil {
		s.log.Warn("sandbox: fork failed", "instance", instance, "err", err)
		return refuse("failed")
	}
	snap := forked.Snapshot
	s.publishSandboxCapture(ctx, snap.StorageKey)
	var volumeBackup string
	if forked.Volume != nil {
		s.uploadVolumeBackup(ctx, forked.Volume)
		volumeBackup = forked.Volume.ID
	}
	return &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Forked{Forked: &vmmdpb.RunSandboxForked{
		Snapshot: &vmmdpb.AgentRunSnapshot{StorageKey: snap.StorageKey, VmstateStorageKey: snap.VMStateStorageKey,
			FcVersion: snap.FCVersion, MemBytes: snap.MemBytes},
		VmstateBytes: forked.Info.VMStateBytes, ForkMs: time.Since(started).Milliseconds(), HasBacking: forked.HasBacking,
		VolumeBackupId: volumeBackup}}}
}
