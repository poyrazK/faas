package agentd

// adr: 20261122132055 §3 — forks. apid queues forks of a session; a paused
// source's forks restore its pause capture, and a running source is asked
// for a fork capture, which agentd takes here without ending it. A fork
// starts once the shared store holds its whole capture.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const (
	// sandboxForkRetry is how long a refused fork waits before the next try.
	sandboxForkRetry = 5 * time.Second
	// sandboxForkTimeout bounds a fork vmmd never answers (one from before
	// forks); the request stays and is tried again.
	sandboxForkTimeout = 10 * time.Minute
)

// forkDue reports whether apid waits for a fork capture of the running
// session and none is being taken.
func (s *Sandboxes) forkDue(ss *session, now time.Time) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.forking && now.Sub(ss.forkAsked) > sandboxForkTimeout {
		ss.forking, ss.forkID, ss.forkRetryAt = false, "", now.Add(sandboxForkRetry)
	}
	return ss.lease.ForkRequested && ss.ready && ss.end == nil && !ss.forking && !ss.pausing && !ss.paused &&
		!now.Before(ss.forkRetryAt)
}

// fork asks vmmd for a fork capture of the running session.
func (s *Sandboxes) fork(ss *session, send func(*scheddpb.SandboxRunRequest) error) {
	captureID := uuid.NewString()
	ss.mu.Lock()
	ss.forking, ss.forkID, ss.forkAsked = true, captureID, s.now()
	ss.mu.Unlock()
	s.log.Info("agentd: forking sandbox", "sandbox_id", ss.claim.ID, "capture_id", captureID)
	if err := send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Fork{Fork: &scheddpb.SandboxRunFork{CaptureId: captureID}}}); err != nil {
		s.forkRefused(ss, "send failed")
	}
}

// markForked records vmmd's fork capture: the forks waiting for one
// restore it once it is uploaded. The session runs on.
func (s *Sandboxes) markForked(ctx context.Context, ss *session, f *scheddpb.SandboxRunForked) error {
	ss.mu.Lock()
	captureID, node := ss.forkID, ss.placedNode
	ss.mu.Unlock()
	if captureID == "" {
		return errors.New("vmmd forked a session agentd did not fork")
	}
	if id := f.GetVolumeBackupId(); id != "" {
		// The fork's volume backup uploads after the fork, like a pause's.
		if _, err := s.store.RecordSandboxVolumeBackup(ctx, ss.claim.ID, ss.claim.LeaseToken, agentstore.SandboxVolumeBackup{
			ID: id, NodeID: node, Reason: agentstore.SandboxVolumeBackupFork, Uploading: true}); err != nil {
			return err
		}
	}
	capture := agentstore.SandboxCapture{ID: captureID, NodeID: node, SessionID: ss.claim.ID, AccountID: ss.claim.AccountID,
		StorageKey: f.GetStorageKey(), VMStateStorageKey: f.GetVmstateStorageKey(), FCVersion: f.GetFcVersion(), MemBytes: f.GetMemBytes(),
		VMStateBytes: f.GetVmstateBytes(), HasBacking: f.GetHasBacking(), VolumeBackupID: f.GetVolumeBackupId()}
	forks, err := s.store.RecordSandboxFork(ctx, ss.claim.ID, ss.claim.LeaseToken, capture)
	if err != nil {
		return err
	}
	if forks > 0 {
		go s.watchForkUpload(ctx, capture)
	}
	ss.mu.Lock()
	ss.forking, ss.forkID = false, ""
	// The renewal that asked is stale until the next one.
	ss.lease.ForkRequested = false
	ss.mu.Unlock()
	s.log.Info("agentd: sandbox forked", "sandbox_id", ss.claim.ID, "node", node, "capture_id", captureID, "forks", forks,
		"mem_bytes", f.GetMemBytes(), "fork_ms", f.GetForkMs())
	return nil
}

// forkRefused leaves the request for the next try after sandboxForkRetry.
func (s *Sandboxes) forkRefused(ss *session, reason string) {
	ss.mu.Lock()
	ss.forking, ss.forkID, ss.forkRetryAt = false, "", s.now().Add(sandboxForkRetry)
	ss.mu.Unlock()
	s.log.Info("agentd: sandbox fork refused", "sandbox_id", ss.claim.ID, "reason", reason)
}

// sweepForks marks the captures forks wait for uploaded once the shared
// store holds them, consumes fork captures no fork waits for, and fails
// forks whose source ended first.
func (s *Sandboxes) sweepForks(ctx context.Context) error {
	consumed, failed, err := s.store.SweepSandboxForks(ctx)
	if consumed+failed > 0 {
		s.log.Info("agentd: swept sandbox forks", "captures_consumed", consumed, "forks_failed", failed)
	}
	if err != nil || s.scfg.Workspaces == nil {
		return err
	}
	captures, err := s.store.UnuploadedSandboxForkCaptures(ctx, sandboxCaptureSweepBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range captures {
		if _, err := s.markForkCaptureUploaded(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("check fork capture %s: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}
