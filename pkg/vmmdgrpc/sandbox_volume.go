package vmmdgrpc

// adr: 20261122132055 §4 — a persistent session's /workspace is a volume on
// its node. vmmd makes it or grows it before the boot, restores it from the
// session's newest backup when the node's copy is missing or older, backs it
// up every volume_backup_interval_ms while the VM runs and once more as a
// pause leaves it, and deletes it when schedd asks for agentd.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

// sandboxVolumeBackupMinEvery bounds how often a running volume is backed
// up, whatever the caller asks.
const sandboxVolumeBackupMinEvery = time.Minute

// WithSandboxVolumes keeps persistent sessions' volumes in v; their backups
// go to the sandbox workspace store (WithSandboxWorkspaces).
func (s *Server) WithSandboxVolumes(v *fcvm.SandboxVolumes) *Server {
	s.sandboxVolumes = v
	return s
}

// sandboxVolumeFromProto checks the start's volume fields: a size within
// the plan, never with a task.
func sandboxVolumeFromProto(start *vmmdpb.RunSandboxStart, plan api.Plan) (gb int, backupID string, every time.Duration, problem *api.Problem) {
	gb, backupID = int(start.GetVolumeGb()), start.GetVolumeBackupId()
	if source := start.GetVolumeSourceSandboxId(); source != "" && (backupID == "" || !start.GetSessionCapture()) {
		return 0, "", 0, api.NewProblem(int(codes.InvalidArgument), api.CodeValidation, "Invalid sandbox session",
			"volume_source_sandbox_id goes with a fork's capture and its volume backup")
	}
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(int(codes.InvalidArgument), api.CodeValidation, "Invalid sandbox session", detail)
	}
	if gb == 0 {
		if backupID != "" || start.GetVolumeBackupIntervalMs() != 0 {
			return 0, "", 0, invalid("volume_backup_id and volume_backup_interval_ms need volume_gb")
		}
		return 0, "", 0, nil
	}
	maxGB := fcvm.SandboxVolumeMaxGB
	if idx := plan.PlanIndex(); idx >= 0 && idx < len(api.SandboxVolumeMaxGB) && api.SandboxVolumeMaxGB[idx] < maxGB {
		maxGB = api.SandboxVolumeMaxGB[idx]
	}
	switch {
	case gb < fcvm.SandboxVolumeMinGB || gb > maxGB:
		return 0, "", 0, invalid(fmt.Sprintf("volume_gb %d is outside 1..%d for the %s plan", gb, maxGB, plan))
	case start.GetTask() != nil:
		return 0, "", 0, invalid("a sandbox task has no volume")
	case backupID != "" && uuid.Validate(backupID) != nil:
		return 0, "", 0, invalid("volume_backup_id must be a UUID")
	case start.GetVolumeBackupIntervalMs() < 0:
		return 0, "", 0, invalid("volume_backup_interval_ms is negative")
	}
	if ms := start.GetVolumeBackupIntervalMs(); ms > 0 {
		every = max(time.Duration(ms)*time.Millisecond, sandboxVolumeBackupMinEvery)
	}
	return gb, backupID, every, nil
}

// prepareSandboxVolume gives wake the session's volume at gb: the node's
// copy when it is the newest backup or newer, otherwise the newest backup
// restored, otherwise a new one. A volume it cannot prepare is Unavailable:
// the session waits and is tried again, never booted without its files.
func (s *Server) prepareSandboxVolume(ctx context.Context, wake *fcvm.SandboxWakeRequest, gb int, backupID, source string) error {
	if source == "" {
		source = wake.SandboxID
	}
	v := s.sandboxVolumes
	if v == nil {
		return status.Error(codes.Unimplemented, "this vmmd keeps no sandbox volumes")
	}
	unavailable := func(err error) error {
		s.log.Warn("sandbox: volume unavailable", "sandbox_id", wake.SandboxID, "backup_id", backupID, "err", err)
		return status.Error(codes.Unavailable, "the session's workspace volume could not be prepared")
	}
	local := v.Exists(wake.AccountID, wake.SandboxID)
	if local && backupID != "" {
		last, ok := v.LastBackup(wake.AccountID, wake.SandboxID)
		// A backup this node cloned the volume for (a pause's, still
		// uploading or given up) is the node's copy itself.
		if (!ok || last.ID != backupID) && !v.TookBackup(wake.AccountID, wake.SandboxID, backupID) {
			if s.sandboxStorage == nil {
				return unavailable(errors.New("no store"))
			}
			want, err := volbackup.ReadManifest(ctx, s.sandboxStorage, wake.AccountID, source, backupID)
			if err != nil {
				return unavailable(err)
			}
			// The node's copy is newer than the newest backup only when this
			// node backed it up last.
			local = ok && !last.CreatedAt.Before(want.CreatedAt)
		}
	}
	if !local && backupID != "" {
		if s.sandboxStorage == nil {
			return unavailable(errors.New("no store"))
		}
		started := time.Now()
		_, err := v.RestoreBackupFrom(ctx, s.sandboxStorage, wake.AccountID, source, wake.SandboxID, backupID)
		s.ops.Observe("SandboxVolumeRestore", time.Since(started), err)
		if err != nil {
			return unavailable(err)
		}
	}
	path, _, err := v.Ensure(ctx, wake.AccountID, wake.SandboxID, gb)
	if errors.Is(err, fcvm.ErrSandboxVolumeShrink) {
		return status.Error(codes.InvalidArgument, "a sandbox volume never shrinks")
	}
	if err != nil {
		return unavailable(err)
	}
	wake.Volume = path
	return nil
}

// backupSandboxVolume backs a running session's volume up unless a backup
// of it is already running, and reports it on the stream.
func (s *Server) backupSandboxVolume(ctx context.Context, act *sandboxActivity) {
	if s.sandboxVolumes == nil || s.sandboxStorage == nil || !act.volumeBackingUp.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer act.volumeBackingUp.Store(false)
		id := uuid.NewString()
		started := time.Now()
		b, err := s.sandboxVolumes.Backup(ctx, s.sandboxStorage, act.accountID, act.sandboxID, id)
		s.ops.Observe("SandboxVolumeBackup", time.Since(started), err)
		if err != nil {
			s.log.Warn("sandbox: volume backup failed", "sandbox_id", act.sandboxID, "err", err)
			return
		}
		ev := &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_VolumeBackup{VolumeBackup: volumeBackupEvent(b, time.Since(started))}}
		select {
		case act.outbox <- ev:
		case <-act.done:
		case <-ctx.Done():
		}
	}()
}

func volumeBackupEvent(b fcvm.SandboxVolumeBackup, took time.Duration) *vmmdpb.RunSandboxVolumeBackup {
	return &vmmdpb.RunSandboxVolumeBackup{BackupId: b.Manifest.ID, SizeBytes: b.Manifest.SizeBytes,
		DataChunks: int32(len(b.Manifest.ChunkSet())), UploadedBytes: b.Stats.BytesUploaded, //nolint:gosec // G115: chunk count of a volume ≤ 1 TB.
		BackupMs: took.Milliseconds(), Incremental: b.Incremental}
}

// prepareSandboxPauseBackup clones a paused session's volume as its
// capture left it and uploads the clone in the background; it returns the
// backup's ID, or "" when the clone failed and only the node holds the
// volume.
func (s *Server) prepareSandboxPauseBackup(ctx context.Context, act *sandboxActivity) string {
	if !act.volume || s.sandboxVolumes == nil || s.sandboxStorage == nil {
		return ""
	}
	p, err := s.sandboxVolumes.PrepareBackup(act.accountID, act.sandboxID, uuid.NewString())
	if err != nil {
		s.log.Warn("sandbox: pause volume backup failed", "sandbox_id", act.sandboxID, "err", err)
		return ""
	}
	s.uploadVolumeBackup(ctx, p)
	return p.ID
}

// uploadVolumeBackup uploads a prepared backup in the background, retrying
// as capture uploads do.
func (s *Server) uploadVolumeBackup(ctx context.Context, p *fcvm.PendingVolumeBackup) {
	started := time.Now()
	s.retryInBackground(ctx, "SandboxVolumeBackup", "volume backup", p.ID, func(ctx context.Context) error {
		_, err := p.Upload(ctx, s.sandboxStorage)
		return err
	}, func() {
		s.log.Info("sandbox: pause volume backup uploaded", "sandbox_id", p.SandboxID, "backup_id", p.ID,
			"upload_ms", time.Since(started).Milliseconds())
	}, p.Discard)
}

// PublishPendingSandboxVolumeBackups uploads the volume backups a restart
// left on this node. vmmd runs it once at start.
func (s *Server) PublishPendingSandboxVolumeBackups(ctx context.Context) {
	if s.sandboxVolumes == nil || s.sandboxStorage == nil {
		return
	}
	for _, p := range s.sandboxVolumes.PendingBackups() {
		s.uploadVolumeBackup(ctx, p)
	}
}

// DeleteSandboxVolume removes a session's volume from this node, unless a
// running session uses it.
func (s *Server) DeleteSandboxVolume(_ context.Context, req *vmmdpb.DeleteSandboxVolumeRequest) (*vmmdpb.DeleteSandboxVolumeResponse, error) {
	if s.sandboxVolumes == nil {
		return nil, status.Error(codes.Unimplemented, "this vmmd keeps no sandbox volumes")
	}
	if uuid.Validate(req.GetAccountId()) != nil || uuid.Validate(req.GetSandboxId()) != nil {
		return nil, status.Error(codes.InvalidArgument, "account_id and sandbox_id must be UUIDs")
	}
	inUse := false
	s.sandboxRuns.Range(func(_, value any) bool {
		if act, ok := value.(*sandboxActivity); ok && act.sandboxID == req.GetSandboxId() {
			inUse = true
		}
		return !inUse
	})
	if inUse {
		return nil, status.Error(codes.FailedPrecondition, "a running session uses this volume")
	}
	existed := s.sandboxVolumes.Exists(req.GetAccountId(), req.GetSandboxId())
	if err := s.sandboxVolumes.Remove(req.GetAccountId(), req.GetSandboxId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if existed {
		s.log.Info("sandbox: volume deleted", "sandbox_id", req.GetSandboxId())
	}
	return &vmmdpb.DeleteSandboxVolumeResponse{Deleted: existed}, nil
}
