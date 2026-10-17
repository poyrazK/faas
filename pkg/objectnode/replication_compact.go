package objectnode

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type CompactionResult struct {
	Status        ReplicationStatus `json:"status"`
	Before        JournalUsage      `json:"before"`
	After         JournalUsage      `json:"after"`
	RecordsPruned int64             `json:"records_pruned"`
	BytesPruned   int64             `json:"bytes_pruned"`
}

// CompactJournal is offline and holds the node's exclusive process lock. A
// negative through selects the largest safe prefix; explicit cuts never bypass
// follower pins. Both primary and fully applied follower disks can be compacted.
func CompactJournal(ctx context.Context, directory string, through int64) (CompactionResult, error) {
	return compactJournal(ctx, directory, through, nil)
}

func compactJournal(ctx context.Context, directory string, through int64, checkpoint func(string) error) (CompactionResult, error) {
	if through < -1 || through > api.ObjectNodeReplicationMaxSequence {
		return CompactionResult{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return CompactionResult{}, err
	}
	s, err := openReplica(directory)
	if err != nil {
		return CompactionResult{}, err
	}
	defer func() { _ = s.Close() }()
	if s.replication.Role == "fenced" {
		return CompactionResult{}, ErrFenced
	}
	if s.replication.Role == "primary" {
		if err = s.finishReplicationPending(); err != nil {
			return CompactionResult{}, err
		}
	}
	if s.replication.ReceivedSequence != s.replication.AppliedSequence {
		return CompactionResult{}, ErrReplicaPending
	}
	cut := through
	safe := retentionSafeSequence(s.replication.ReplicationStatus)
	if cut == -1 {
		cut = safe
	}
	if cut < s.replication.BaseSequence || cut > s.replication.AppliedSequence {
		return CompactionResult{}, ErrInvalid
	}
	if cut > safe {
		return CompactionResult{}, ErrRetentionPinned
	}
	// No ordinary startup recovery runs here: compaction must remain available
	// when a retention ceiling interrupted a multi-record receipt transaction.
	if err = s.verifyCompaction(ctx); err != nil {
		return CompactionResult{}, err
	}
	result := CompactionResult{Before: s.journalUsage}
	state := *s.replication
	hash := state.BaseHash
	if cut != state.BaseSequence {
		e, readErr := s.readJournal(cut)
		if readErr != nil {
			return result, readErr
		}
		hash = e.Hash
	}
	state.Version, state.BaseSequence, state.BaseHash = 4, cut, hash
	state.Checkpoint = &JournalCheckpoint{Version: 1, Sequence: cut, Hash: hash, CreatedAt: time.Now().UTC(), Pruning: true}
	if p := state.Durability; p != nil && p.Mode == DurabilityLocal && p.ConfirmedSequence < cut {
		state.Durability = nil // Old local-mode telemetry is no longer an active promise.
	}
	if err = backupCheckpoint(ctx, checkpoint, "before checkpoint"); err != nil {
		return result, err
	}
	if err = s.saveReplicationState(state); err != nil {
		return result, err
	}
	if err = backupCheckpoint(ctx, checkpoint, "after checkpoint"); err != nil {
		return result, err
	}
	if err = s.resumeCompaction(ctx, checkpoint); err != nil {
		return result, err
	}
	result.Status, result.After = replicationSnapshot(s.replication.ReplicationStatus), s.journalUsage
	result.RecordsPruned = result.Before.Records - result.After.Records
	result.BytesPruned = result.Before.JournalBytes + result.Before.BlobBytes - result.After.JournalBytes - result.After.BlobBytes
	return result, nil
}

// Vet every component before reading metadata through nested paths. Compaction
// never deletes or rewrites anything outside replication/{log,blobs,state.json}.
func (s *Store) verifyCompaction(ctx context.Context) error {
	if err := s.vetCompactionTree(ctx); err != nil {
		return err
	}
	buffer := make([]byte, api.ObjectNodeBackupBufferBytes)
	return walkBackupTree(ctx, s.root, "", 0, func(path string, info os.FileInfo) error {
		return s.verifyCompactionFile(ctx, path, info, buffer)
	})
}

func (s *Store) vetCompactionTree(ctx context.Context) error {
	count := 0
	return walkBackupTree(ctx, s.root, "", 0, func(_ string, _ os.FileInfo) error {
		count++
		if count > api.ObjectNodeBackupMaxEntries+3*api.ObjectNodeReplicationMaxEntries {
			return ErrCapacity
		}
		return nil
	})
}

func (s *Store) verifyCompactionFile(ctx context.Context, path string, info os.FileInfo, buffer []byte) error {
	if info.IsDir() {
		return nil
	}
	if strings.HasPrefix(path, "replication/blobs/") {
		digest, err := copyBackupFile(ctx, filepath.Join(s.root, path), "", info.Size(), buffer)
		if err != nil {
			return err
		}
		return s.verifyBackupDigest(path, info.Size(), digest)
	}
	if !strings.HasPrefix(path, "replication/") && (strings.HasSuffix(path, "/current.json") || strings.HasSuffix(path, "/bucket.json") || strings.HasSuffix(path, "/session.json") || strings.HasPrefix(path, "receipts/") && strings.HasSuffix(path, ".json")) {
		body, err := readCompactionMetadata(filepath.Join(s.root, path))
		if err != nil {
			return err
		}
		blob, err := replicationMetadata(path, body)
		if err != nil || blob == nil {
			if err == nil && strings.HasPrefix(path, "multipart/") && strings.HasSuffix(path, "/session.json") {
				return s.verifyCompactionSession(path)
			}
			return err
		}
		digest, err := copyBackupFile(ctx, filepath.Join(s.root, blob.Path), "", blob.Size, buffer)
		if err == nil && digest != blob.SHA256 {
			return ErrDigest
		}
		return err
	}
	return nil
}

func (s *Store) verifyCompactionSession(path string) error {
	u, err := s.readMultipart(strings.Split(path, "/")[1])
	if err != nil || u.Status != multipartCompleting {
		return err
	}
	m, _, err := s.completionManifest(u, MultipartCompletion{Parts: u.Completion.Parts, IfMatch: u.Completion.IfMatch, IfNoneMatch: u.Completion.IfNoneMatch})
	if err == nil && m.Hash() != u.Completion.Hash() {
		return ErrCorrupt
	}
	return err
}

func readCompactionMetadata(path string) ([]byte, error) {
	f, err := openBackupFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if len(body) > maxRecordBytes {
		return nil, ErrCorrupt
	}
	return body, err
}

func (s *Store) retainedBlobReferences(ctx context.Context) (map[string]bool, error) {
	keep := make(map[string]bool)
	for sequence := s.replication.BaseSequence + 1; sequence <= s.replication.ReceivedSequence; sequence++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e, err := s.readJournal(sequence)
		if err != nil {
			return nil, err
		}
		if e.Blob != nil {
			keep[e.Blob.SHA256] = true
		}
	}
	// Pending receipt recovery is independent of retained journal positions.
	// Preserve its replay payload even when its intent lies below the new floor.
	err := visitDirectory(filepath.Join(s.root, "receipts"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(e.Name(), ".tmp-") && validReceiptID(strings.TrimPrefix(e.Name(), ".tmp-")) {
			return nil
		}
		r, err := s.readReceipt(strings.TrimSuffix(e.Name(), ".json"))
		if err == nil && r.Status == receiptCommitting {
			keep[r.Object.SHA256] = true
		}
		return err
	})
	return keep, err
}

func (s *Store) resumeCompaction(ctx context.Context, checkpoint func(string) error) error {
	c := s.replication.Checkpoint
	if c == nil || !c.Pruning {
		return nil
	}
	if err := s.vetCompactionTree(ctx); err != nil {
		return err
	}
	keep, err := s.retainedBlobReferences(ctx)
	if err != nil {
		return err
	}
	if err = visitDirectory(s.replicationPath("log"), func(e os.DirEntry) error {
		sequence, parseErr := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if parseErr != nil || journalName(sequence) != e.Name() || e.IsDir() {
			return ErrCorrupt
		}
		if sequence <= c.Sequence {
			if err := os.Remove(s.replicationPath("log/" + e.Name())); err != nil {
				return err
			}
			return backupCheckpoint(ctx, checkpoint, "pruned record")
		}
		return ctx.Err()
	}); err != nil {
		return err
	}
	if err = visitDirectory(s.replicationPath("blobs"), func(e os.DirEntry) error {
		digest := strings.TrimSuffix(e.Name(), ".data")
		if !validScrubFindingID(digest) || digest+".data" != e.Name() || e.IsDir() {
			return ErrCorrupt
		}
		if !keep[digest] {
			if err := os.Remove(s.replicationPath("blobs/" + e.Name())); err != nil {
				return err
			}
			return backupCheckpoint(ctx, checkpoint, "pruned blob")
		}
		return ctx.Err()
	}); err != nil {
		return err
	}
	for _, directory := range []string{"log", "blobs"} {
		if err = syncDirectory(s.replicationPath(directory)); err != nil {
			return err
		}
	}
	if err = backupCheckpoint(ctx, checkpoint, "cleanup synced"); err != nil {
		return err
	}
	state, done := *s.replication, *c
	done.Pruning, state.Checkpoint = false, &done
	if err = s.saveReplicationState(state); err != nil {
		return err
	}
	if err = s.validateReplicationFiles(); err != nil {
		return err
	}
	return backupCheckpoint(ctx, checkpoint, "completed")
}
