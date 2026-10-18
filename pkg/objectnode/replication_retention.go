package objectnode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// JournalCheckpoint anchors the first retained prefix. Pruning is sealed before
// unlinking anything and cleared only after both retention directories are fsynced.
type JournalCheckpoint struct {
	Version   int       `json:"version"`
	Sequence  int64     `json:"sequence"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	Pruning   bool      `json:"pruning"`
}

// JournalUsage is cached under the catalog lock, including orphan payloads.
// These are physical retention budgets, not customer billable storage.
type JournalUsage struct {
	Records      int64 `json:"records"`
	JournalBytes int64 `json:"journal_bytes"`
	Blobs        int64 `json:"blobs"`
	BlobBytes    int64 `json:"blob_bytes"`
}

type RetentionStatus struct {
	Enabled         bool         `json:"enabled"`
	BaseSequence    int64        `json:"base_sequence"`
	AppliedSequence int64        `json:"applied_sequence"`
	SafeSequence    int64        `json:"safe_sequence"`
	Followers       int          `json:"followers"`
	Usage           JournalUsage `json:"usage"`
}

func validJournalRetention(s ReplicationStatus) bool {
	if s.Version < 4 {
		return s.Checkpoint == nil && len(s.Followers) == 0
	}
	c := s.Checkpoint
	if c != nil && (c.Version != 1 || c.Sequence != s.BaseSequence || c.Hash != s.BaseHash || c.CreatedAt.IsZero()) {
		return false
	}
	if len(s.Followers) > api.ObjectNodeReplicationMaxFollowers || s.Role == "follower" && len(s.Followers) != 0 {
		return false
	}
	previous := ""
	for _, f := range s.Followers {
		if !validReceiptID(f.ReplicaID) || f.ReplicaID <= previous || f.ReplicaID == s.NodeID || !validReceiptID(f.RegistrationID) || f.Sequence < s.BaseSequence || f.Sequence > s.AppliedSequence || !validScrubFindingID(f.Hash) || f.UpdatedAt.IsZero() || f.Sequence == s.BaseSequence && f.Hash != s.BaseHash || f.Sequence == s.AppliedSequence && f.Hash != s.AppliedHash {
			return false
		}
		previous = f.ReplicaID
	}
	return true
}

func retentionSafeSequence(s ReplicationStatus) int64 {
	cut := s.AppliedSequence
	for _, follower := range s.Followers {
		cut = min(cut, follower.Sequence)
	}
	if p := s.Durability; p != nil && p.Mode == DurabilityReplica {
		cut = min(cut, max(s.BaseSequence, p.ConfirmedSequence))
	}
	return cut
}

// RetentionStatus never reads the disk, including when used by metrics scrapes.
func (s *Store) RetentionStatus() (RetentionStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return RetentionStatus{}, ErrClosed
	}
	if s.replicationDirty || s.authorityDirty {
		return RetentionStatus{}, ErrCorrupt
	}
	if s.replication == nil {
		return RetentionStatus{}, nil
	}
	state := s.replication
	return RetentionStatus{Enabled: true, BaseSequence: state.BaseSequence, AppliedSequence: state.AppliedSequence, SafeSequence: retentionSafeSequence(state.ReplicationStatus), Followers: len(state.Followers), Usage: s.journalUsage}, nil
}

func (s *Store) validateFollowerPositions() error {
	for _, f := range s.replication.Followers {
		if err := s.checkDurablePosition(f.Sequence, f.Hash); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) persistJournalEntry(e JournalEntry) (bool, error) {
	path := s.replicationPath("log/" + journalName(e.Sequence))
	before, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	body, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	delta := int64(len(body))
	if before != nil {
		if !before.Mode().IsRegular() {
			return false, ErrCorrupt
		}
		delta -= before.Size()
	}
	if before == nil && s.journalUsage.Records >= api.ObjectNodeReplicationMaxEntries || delta > api.ObjectNodeReplicationJournalBytes-s.journalUsage.JournalBytes {
		return false, ErrCapacity
	}
	published, err := s.replicationPersist(path, e)
	if published {
		s.trackRetentionEntry(e)
		s.journalUsage.JournalBytes += delta
		if before == nil {
			s.journalUsage.Records++
		}
	}
	return published, err
}

func (s *Store) canRetainBlob(size int64) error {
	if size < 0 || size > api.ObjectNodeReplicationBlobBytes-s.journalUsage.BlobBytes || s.journalUsage.Blobs >= api.ObjectNodeReplicationMaxEntries {
		return ErrCapacity
	}
	return nil
}

func (s *Store) observeRetentionFile(path string, info os.FileInfo) error {
	if info.IsDir() {
		return nil
	}
	switch {
	case strings.HasPrefix(path, "log/"):
		s.journalUsage.Records++
		s.journalUsage.JournalBytes += info.Size()
	case strings.HasPrefix(path, "blobs/"):
		s.journalUsage.Blobs++
		s.journalUsage.BlobBytes += info.Size()
	}
	return nil
}

// A source floor can advance under persisted pins, including online maintenance.
// A worker which needs an older prefix must use a fresh verified snapshot.
func (s *Store) reportFollowerCursor(ctx context.Context, source ReplicationSource, head ReplicationStatus) error {
	for _, registered := range head.Followers {
		if registered.ReplicaID != s.replication.NodeID {
			continue
		}
		reporter, ok := source.(FollowerReporter)
		if !ok {
			return ErrReplicationOff
		}
		if err := syncDirectory(s.replicationPath("")); err != nil {
			return err
		}
		local := s.replication
		r := FollowerReport{Version: 1, StreamID: local.StreamID, PrimaryID: local.PrimaryID, Epoch: local.Epoch, RegistrationID: registered.RegistrationID, ReplicaID: local.NodeID, Sequence: local.AppliedSequence, Hash: local.AppliedHash}
		cursor, err := reporter.ReportFollower(ctx, r)
		if err == nil && (cursor.RegistrationID != r.RegistrationID || cursor.ReplicaID != r.ReplicaID || cursor.Sequence != r.Sequence || cursor.Hash != r.Hash || cursor.UpdatedAt.IsZero()) {
			return ErrReplicationFork
		}
		return err
	}
	return nil
}
