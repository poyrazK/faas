package agentd

// adr: 20261122132055 — prompt requests and fork starts. apid records a
// pause, close or fork request on the session row; agentd saw it only at the
// lease renewal (every third of the lease TTL, 10 s by default). apid now
// also sends db.NotifySandboxSessionRequest and the replica holding the
// session renews at once. A fork waited for the sweep (SweepInterval, 10 s)
// to see its capture in the shared store; the replica that recorded the
// capture now watches the upload itself. The renewal and the sweep stay the
// fallbacks for a lost notification or a replica that went away.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

const (
	// sandboxForkUploadPoll paces the watch on a fork capture's upload.
	sandboxForkUploadPoll = time.Second
	// sandboxForkUploadWatch bounds it; the sweep takes over after.
	sandboxForkUploadWatch = 5 * time.Minute
)

// sandboxRequestSource is the store's request notifications; a store
// without it leaves agentd on lease renewals.
type sandboxRequestSource interface {
	SandboxSessionRequests(ctx context.Context, log *slog.Logger) (<-chan string, error)
}

// sessionWakes maps the sessions this replica holds to their renewal nudge.
type sessionWakes struct {
	mu sync.Mutex
	m  map[string]chan struct{}
}

// add registers session id and returns its nudge channel and the release.
func (w *sessionWakes) add(id string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	if w.m == nil {
		w.m = map[string]chan struct{}{}
	}
	w.m[id] = ch
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		if w.m[id] == ch {
			delete(w.m, id)
		}
		w.mu.Unlock()
	}
}

// wake nudges session id if this replica holds it.
func (w *sessionWakes) wake(id string) {
	w.mu.Lock()
	ch := w.m[id]
	w.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default: // a renewal is already due
	}
}

// ListenRequests wakes held sessions as apid records requests on them, until
// ctx ends. Run starts it; a caller driving sessions with ClaimOnce starts it
// itself.
func (s *Sandboxes) ListenRequests(ctx context.Context) {
	src, ok := s.store.(sandboxRequestSource)
	if !ok {
		return
	}
	ids, err := src.SandboxSessionRequests(ctx, s.log)
	if err != nil {
		s.log.Warn("agentd: sandbox requests arrive at lease renewals only", "err", err)
		return
	}
	for id := range ids {
		s.wakes.wake(id)
	}
}

// forkCaptureKeys are the shared-store objects a fork capture's forks
// restore: memory, vmstate, private drive, backing and the volume backup.
func forkCaptureKeys(c agentstore.SandboxCapture) []string {
	snap := state.Snapshot{StorageKey: c.StorageKey}
	keys := []string{c.StorageKey, c.VMStateStorageKey, state.SnapshotDriveKey(snap)}
	if c.HasBacking {
		keys = append(keys, state.SnapshotBackingKey(snap))
	}
	if c.VolumeBackupID != "" {
		keys = append(keys, volbackup.ManifestKey(c.AccountID, c.SessionID, c.VolumeBackupID))
	}
	return keys
}

// markForkCaptureUploaded checks the shared store for c's objects and, once
// it holds them all, makes c's forks claimable. It reports whether it did.
func (s *Sandboxes) markForkCaptureUploaded(ctx context.Context, c agentstore.SandboxCapture) (bool, error) {
	held, err := s.storeHoldsAll(ctx, forkCaptureKeys(c))
	if err != nil || !held {
		return false, err
	}
	if err := s.store.MarkSandboxForkCaptureUploaded(ctx, c.ID); err != nil {
		return false, err
	}
	if c.VolumeBackupID != "" {
		if err := s.store.MarkSandboxVolumeBackupStored(ctx, c.VolumeBackupID); err != nil {
			return true, err
		}
	}
	s.log.Info("agentd: sandbox fork capture uploaded", "sandbox_id", c.SessionID, "capture_id", c.ID)
	return true, nil
}

// watchForkUpload polls a fresh fork capture's upload so its forks start as
// soon as the shared store holds it, not at the next sweep.
func (s *Sandboxes) watchForkUpload(ctx context.Context, c agentstore.SandboxCapture) {
	if s.scfg.Workspaces == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxForkUploadWatch)
	defer cancel()
	tick := time.NewTicker(sandboxForkUploadPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		done, err := s.markForkCaptureUploaded(ctx, c)
		if err != nil {
			s.log.Debug("agentd: fork capture upload check", "capture_id", c.ID, "err", err)
			continue
		}
		if done {
			return
		}
	}
}
