package objectnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// ReplicationSource returns authenticated, stream-bound immutable records and
// bounded blob ranges. Implementations must return an error for unavailable data.
type ReplicationSource interface {
	Head(context.Context) (ReplicationStatus, error)
	Entry(context.Context, string, int64) (JournalEntry, error)
	Blob(context.Context, string, string, int64, int64) (io.ReadCloser, error)
}

type CatchUpOptions struct {
	MaxEntries           int
	MaxBytes             int64
	WorkBytesPerSecond   int64
	WorkEntriesPerSecond int
}

// ReplicaFollower owns the follower's process lock for a worker's lifetime.
// Its narrow API cannot expose a writable Store or an S3 listener.
type ReplicaFollower struct{ store *Store }

func OpenFollower(directory string) (*ReplicaFollower, error) {
	s, err := openReplica(directory)
	if err != nil {
		return nil, err
	}
	if s.replication.Role != "follower" {
		_ = s.Close()
		return nil, ErrReplica
	}
	return &ReplicaFollower{store: s}, nil
}

func (f *ReplicaFollower) Close() error { return f.store.Close() }

func (f *ReplicaFollower) CatchUp(ctx context.Context, source ReplicationSource, options CatchUpOptions) (ReplicationProgress, error) {
	if source == nil || !validCatchUpOptions(options) {
		return ReplicationProgress{}, ErrInvalid
	}
	f.store.life.RLock()
	defer f.store.life.RUnlock()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	if f.store.closed {
		return ReplicationProgress{}, ErrClosed
	}
	return f.store.catchUp(ctx, source, options)
}

type ReplicationProgress struct {
	Local             ReplicationStatus      `json:"local"`
	SourceSequence    int64                  `json:"source_sequence"`
	SourceHash        string                 `json:"source_hash"`
	EntriesApplied    int                    `json:"entries_applied"`
	BytesTransferred  int64                  `json:"bytes_transferred"`
	CaughtUp          bool                   `json:"caught_up"`
	ConfirmedSequence *int64                 `json:"confirmed_sequence,omitempty"`
	WaitCursor        *ReplicationWaitCursor `json:"wait_cursor,omitempty"`
	WorkBytes         int64                  `json:"work_bytes,omitempty"`
	WorkWaitMS        float64                `json:"work_wait_ms,omitempty"`
	Profile           ReplicationProfile     `json:"profile"`
}

func openReplica(directory string) (*Store, error) {
	directory, err := backupDirectory(directory)
	if err != nil {
		return nil, err
	}
	s, err := lockStore(directory)
	if err != nil {
		return nil, err
	}
	if err = s.loadReplication(true); err != nil {
		_ = s.Close()
		return nil, err
	}
	if s.replication == nil {
		_ = s.Close()
		return nil, ErrReplicationOff
	}
	if err = s.validateRecoveryFormat(); err != nil && !s.recoverableMissingBarrier() {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func ReadReplicationStatus(directory string) (ReplicationStatus, error) {
	s, err := openReplica(directory)
	if err != nil {
		return ReplicationStatus{}, err
	}
	defer func() { _ = s.Close() }()
	return replicationSnapshot(s.replication.ReplicationStatus), nil
}

// CatchUpReplica performs bounded asynchronous work under the follower's process
// lock. Partial transfers survive interruption; the applied cursor advances only
// after verified bytes, catalog publication and directory fsync.
func CatchUpReplica(ctx context.Context, directory string, source ReplicationSource, options CatchUpOptions) (ReplicationProgress, error) {
	if source == nil || !validCatchUpOptions(options) {
		return ReplicationProgress{}, ErrInvalid
	}
	follower, err := OpenFollower(directory)
	if err != nil {
		return ReplicationProgress{}, err
	}
	defer func() { _ = follower.Close() }()
	return follower.CatchUp(ctx, source, options)
}

func (s *Store) catchUp(ctx context.Context, source ReplicationSource, options CatchUpOptions) (progress ReplicationProgress, err error) {
	started := time.Now()
	defer func() { addReplicaTime(&progress.Profile.TotalMS, started) }()
	s.configureReplicaWork(options)
	if work := s.replicaWork; work != nil {
		before, waited, entryWait := work.total, work.wait, work.entryWait
		defer func() {
			progress.WorkBytes = work.total - before
			progress.WorkWaitMS = float64(work.wait-waited) / float64(time.Millisecond)
			progress.Profile.EntryWaitMS = float64(work.entryWait-entryWait) / float64(time.Millisecond)
			progress.Profile.ByteWaitMS = progress.WorkWaitMS - progress.Profile.EntryWaitMS
		}()
	}
	defer func() { progress.Local = replicationSnapshot(s.replication.ReplicationStatus) }()
	phase := time.Now()
	head, err := source.Head(ctx)
	if err != nil {
		addReplicaTime(&progress.Profile.SourceCheckMS, phase)
		return progress, err
	}
	progress.SourceSequence, progress.SourceHash = head.AppliedSequence, head.AppliedHash
	err = s.checkReplicaSource(ctx, source, head)
	addReplicaTime(&progress.Profile.SourceCheckMS, phase)
	if err != nil {
		return progress, err
	}
	// Report only a successfully fsynced applied checkpoint, including after a
	// bounded/partial step. Failed persistence must never report a confirmation.
	defer func() {
		phase := time.Now()
		defer func() { addReplicaTime(&progress.Profile.ReportMS, phase) }()
		if err == nil {
			err = s.reportFollowerCursor(ctx, source, head)
			if err == nil {
				err = s.reportReplicaConfirmation(ctx, source, head, &progress)
			}
		}
	}()
	phase = time.Now()
	err = s.adoptReplicationRecord()
	addReplicaTime(&progress.Profile.JournalMS, phase)
	if err != nil {
		return progress, err
	}
	if s.replication.ReceivedSequence > head.AppliedSequence {
		return progress, ErrReplicationFork
	}
	budget := options.MaxBytes
	var pending []JournalEntry
	for progress.EntriesApplied < options.MaxEntries && s.replication.AppliedSequence < head.AppliedSequence {
		if err = ctx.Err(); err != nil {
			return progress, err
		}
		if len(pending) == 0 {
			limit := min(options.MaxEntries-progress.EntriesApplied, int(head.AppliedSequence-s.replication.AppliedSequence))
			phase = time.Now()
			pending, err = s.fetchReplicaEntries(ctx, source, head, limit)
			addReplicaTime(&progress.Profile.EntryFetchMS, phase)
			if err != nil {
				return progress, err
			}
		}
		e := pending[0]
		pending = pending[1:]
		phase = time.Now()
		err = s.receiveEntry(e)
		addReplicaTime(&progress.Profile.JournalMS, phase)
		if err != nil {
			return progress, err
		}
		if e.Blob != nil {
			phase = time.Now()
			n, ready, err := s.downloadReplicaBlob(ctx, source, e.Blob, &budget, &progress.Profile)
			addReplicaTime(&progress.Profile.DownloadMS, phase)
			progress.BytesTransferred += n
			if err != nil {
				return progress, err
			}
			if !ready {
				return progress, nil
			}
		}
		phase = time.Now()
		err = s.applyJournal(ctx, e)
		addReplicaTime(&progress.Profile.ApplyMS, phase)
		if err != nil {
			return progress, err
		}
		state := *s.replication
		state.AppliedSequence, state.AppliedHash = e.Sequence, e.Hash
		phase = time.Now()
		err = s.saveReplicationState(state)
		addReplicaTime(&progress.Profile.CheckpointMS, phase)
		if err != nil {
			return progress, err
		}
		progress.EntriesApplied++
	}
	progress.CaughtUp = s.replication.AppliedSequence == head.AppliedSequence && s.replication.AppliedHash == head.AppliedHash
	if progress.CaughtUp {
		cursor := replicationWaitCursor(head)
		progress.WaitCursor = &cursor
	}
	return progress, nil
}

func checkSourcePosition(ctx context.Context, source ReplicationSource, head ReplicationStatus, sequence int64, hash string) error {
	if sequence < head.BaseSequence {
		return ErrReplicaReseed
	}
	if sequence > head.AppliedSequence {
		return ErrReplicationFork
	}
	if sequence == head.BaseSequence && hash == head.BaseHash {
		return nil
	}
	if sequence == 0 {
		if hash != replicationGenesis(head.StreamID) {
			return ErrReplicationFork
		}
		return nil
	}
	e, err := source.Entry(ctx, head.StreamID, sequence)
	if errors.Is(err, ErrReplicaReseed) {
		return checkAdvancedSourceFloor(ctx, source, head, sequence, hash)
	}
	if err != nil {
		return err
	}
	if !validJournalEntry(e) || e.StreamID != head.StreamID || e.Sequence != sequence || e.Hash != hash || sequence == head.AppliedSequence && hash != head.AppliedHash {
		return ErrReplicationFork
	}
	return nil
}

// Online compaction may move the floor to this valid position between Head
// and Entry. Re-read only to accept an exact new floor, never to skip history.
func checkAdvancedSourceFloor(ctx context.Context, source ReplicationSource, old ReplicationStatus, sequence int64, hash string) error {
	fresh, err := source.Head(ctx)
	if err != nil {
		return err
	}
	if !validReplicationStatus(fresh) || fresh.Role == "follower" || old.Role == "fenced" && fresh.Role != "fenced" || fresh.StreamID != old.StreamID || fresh.NodeID != old.NodeID || fresh.PrimaryID != old.PrimaryID || fresh.Epoch != old.Epoch || fresh.BaseSequence < old.BaseSequence || fresh.AppliedSequence < old.AppliedSequence || fresh.AppliedSequence == old.AppliedSequence && fresh.AppliedHash != old.AppliedHash {
		return ErrReplicationFork
	}
	if sequence < fresh.BaseSequence {
		return ErrReplicaReseed
	}
	if sequence == fresh.BaseSequence && hash == fresh.BaseHash {
		return nil
	}
	return ErrReplicationFork
}

func (s *Store) receiveEntry(e JournalEntry) error {
	if s.replication.ReceivedSequence > s.replication.AppliedSequence {
		if s.replication.ReceivedSequence != e.Sequence || s.replication.ReceivedHash != e.Hash {
			return ErrReplicationFork
		}
		return nil
	}
	if err := backupSpace(s.replicationPath("log"), api.ObjectNodeReplicationRecordMaxBytes); err != nil {
		return err
	}
	if _, err := s.persistJournalEntry(e); err != nil {
		return err
	}
	return s.adoptReplicationRecord()
}

func (s *Store) downloadReplicaBlob(ctx context.Context, source ReplicationSource, blob *ReplicationBlob, budget *int64, profile *ReplicationProfile) (int64, bool, error) {
	final := s.replicationPath("blobs/" + blob.SHA256 + ".data")
	if info, err := os.Lstat(final); err == nil {
		if !info.Mode().IsRegular() || info.Size() != blob.Size {
			return 0, false, ErrCorrupt
		}
		started := time.Now()
		digest, err := copyBackupFileBudget(ctx, final, "", blob.Size, make([]byte, api.ObjectNodeBackupBufferBytes), s.replicaWork)
		addReplicaTime(&profile.VerificationMS, started)
		if err != nil {
			return 0, false, err
		}
		if digest != blob.SHA256 {
			return 0, false, ErrDigest
		}
		if err = syncDirectory(filepath.Dir(final)); err != nil {
			return 0, false, err
		}
		if err = syncDirectory(s.replicationPath("transfer")); err != nil {
			return 0, false, err
		}
		return 0, true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, false, err
	}
	if err := s.canRetainBlob(blob.Size); err != nil {
		return 0, false, err
	}
	part := s.replicationPath("transfer/" + blob.SHA256 + ".part")
	fd, err := unix.Open(part, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return 0, false, err
	}
	f := os.NewFile(uintptr(fd), part)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > blob.Size {
		return 0, false, ErrCorrupt
	}
	offset, transferred := info.Size(), int64(0)
	for offset < blob.Size && *budget > 0 {
		take := min(api.ObjectNodeReplicationChunkBytes, *budget, blob.Size-offset)
		if err = s.replicaWork.waitBytes(ctx, int(take)); err != nil {
			return transferred, false, err
		}
		if err = backupSpace(filepath.Dir(part), take); err != nil {
			return transferred, false, err
		}
		body, err := source.Blob(ctx, s.replication.StreamID, blob.SHA256, offset, take)
		if err != nil {
			return transferred, false, err
		}
		if _, err = f.Seek(offset, io.SeekStart); err != nil {
			_ = body.Close()
			return transferred, false, err
		}
		n, copyErr := io.CopyBuffer(f, io.LimitReader(contextReader{ctx, body}, take+1), make([]byte, api.ObjectNodeScrubBufferBytes))
		closeErr := body.Close()
		transferred += n
		*budget -= n
		if err = f.Sync(); err != nil {
			return transferred, false, err
		}
		if n > take {
			_ = f.Close()
			_ = os.Remove(part)
			return transferred, false, ErrCorrupt
		}
		if copyErr != nil {
			return transferred, false, copyErr
		}
		if closeErr != nil {
			return transferred, false, closeErr
		}
		if n != take {
			return transferred, false, io.ErrUnexpectedEOF
		}
		offset += n
	}
	if offset != blob.Size {
		return transferred, false, nil
	}
	if err = f.Sync(); err != nil {
		return transferred, false, err
	}
	if err = f.Close(); err != nil {
		return transferred, false, err
	}
	started := time.Now()
	digest, err := copyBackupFileBudget(ctx, part, "", blob.Size, make([]byte, api.ObjectNodeBackupBufferBytes), s.replicaWork)
	addReplicaTime(&profile.VerificationMS, started)
	if err != nil {
		return transferred, false, err
	}
	if digest != blob.SHA256 {
		_ = os.Remove(part)
		_ = syncDirectory(filepath.Dir(part))
		return transferred, false, ErrDigest
	}
	if err = renameNoReplace(part, final); err != nil {
		return transferred, false, err
	}
	s.journalUsage.Blobs++
	s.journalUsage.BlobBytes += blob.Size
	if err = syncDirectory(filepath.Dir(final)); err != nil {
		return transferred, false, err
	}
	if err = syncDirectory(filepath.Dir(part)); err != nil {
		return transferred, false, err
	}
	return transferred, true, nil
}

func replicationError(status int) error {
	return fmt.Errorf("object replication: source returned HTTP %d", status)
}
