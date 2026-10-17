package objectnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func writeReplicationJSON(path string, value any) (bool, error) {
	return writeJSONLimit(path, value, api.ObjectNodeReplicationRecordMaxBytes)
}

func readReplicationJSON(path string, value any) error {
	f, err := openBackupFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, api.ObjectNodeReplicationRecordMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > api.ObjectNodeReplicationRecordMaxBytes {
		return ErrCorrupt
	}
	return decodeBackupRecord(body, value)
}

func (s *Store) readJournal(sequence int64) (JournalEntry, error) {
	var e JournalEntry
	if sequence < 1 || sequence > api.ObjectNodeReplicationMaxSequence {
		return e, ErrInvalid
	}
	err := readReplicationJSON(s.replicationPath("log/"+journalName(sequence)), &e)
	if err != nil {
		return e, err
	}
	if !validJournalEntry(e) || e.Sequence != sequence || s.replication == nil || e.StreamID != s.replication.StreamID || !journalAuthority(e, s.replication.ReplicationStatus) {
		return e, ErrCorrupt
	}
	return e, nil
}

func nodeFormatVersion(root string) (int, error) {
	var format struct {
		Engine  string `json:"engine"`
		Version int    `json:"version"`
	}
	if err := readJSON(filepath.Join(root, "format.json"), &format); err != nil {
		return 0, err
	}
	if format.Engine != "gregale-object-node" || format.Version < 3 || format.Version > 5 {
		return 0, ErrCorrupt
	}
	return format.Version, nil
}

func writeReplicationFormat(root string) error {
	if version, err := nodeFormatVersion(root); err == nil && version == 5 {
		return nil // Enabling/repairing replication must never downgrade retention semantics.
	}
	_, err := writeJSON(filepath.Join(root, "format.json"), struct {
		Engine  string `json:"engine"`
		Version int    `json:"version"`
	}{"gregale-object-node", 4})
	return err
}

func (s *Store) readReplicationState() (replicationState, error) {
	var state replicationState
	err := readReplicationJSON(s.replicationPath("state.json"), &state)
	if err != nil {
		return state, err
	}
	if !validReplicationStatus(state.ReplicationStatus) || state.Seal != sealReplication(state).Seal {
		return state, ErrCorrupt
	}
	return state, nil
}

func (s *Store) saveReplicationState(state replicationState) error {
	state.UpdatedAt = time.Now().UTC()
	state = sealReplication(state)
	_, err := s.replicationPersist(s.replicationPath("state.json"), state)
	if err == nil {
		s.replication = &state
		s.signalReplicationChange()
	}
	return err
}

// Called before ordinary startup recovery. No follower may run the primary's
// receipt settlement, multipart expiry, garbage collector, workers or listener.
//
//nolint:contextcheck // Startup must complete a sealed pruning decision under the process lock before Open returns.
func (s *Store) loadReplication(allowFollower bool) error {
	version, err := nodeFormatVersion(s.root)
	if err != nil {
		return err
	}
	if err = visitDirectory(s.root, func(e os.DirEntry) error {
		if strings.HasPrefix(e.Name(), ".replication-init-") && validReceiptID(strings.TrimPrefix(e.Name(), ".replication-init-")) {
			if !e.IsDir() {
				return ErrCorrupt
			}
			return os.RemoveAll(filepath.Join(s.root, e.Name()))
		}
		return nil
	}); err != nil {
		return err
	}
	info, err := os.Lstat(s.replicationPath(""))
	if errors.Is(err, os.ErrNotExist) && version == 3 {
		return nil
	}
	if err != nil || !info.IsDir() {
		return ErrCorrupt
	}
	state, err := s.readReplicationState()
	if err != nil {
		return err
	}
	s.replication = &state
	s.initRetentionIndex()
	if state.Role == "follower" && !allowFollower {
		return ErrReplica
	}
	// A crash during opt-in may publish the empty primary journal before the
	// format marker. It is safe to complete that initial transition only at 0.
	if version == 3 {
		if state.Role != "primary" || state.ReceivedSequence != 0 {
			return ErrCorrupt
		}
		if err = writeReplicationFormat(s.root); err != nil {
			return err
		}
	}
	for _, dir := range []string{"log", "blobs", "transfer"} {
		info, err = os.Lstat(s.replicationPath(dir))
		if err != nil || !info.IsDir() {
			return ErrCorrupt
		}
	}
	if err = s.validateReplicationFiles(); err != nil {
		return err
	}
	previous, epoch := state.BaseHash, int64(1)
	for sequence := state.BaseSequence + 1; sequence <= state.ReceivedSequence; sequence++ {
		e, err := s.readJournal(sequence)
		if err != nil || e.Previous != previous || journalEpoch(e) < epoch {
			return ErrCorrupt
		}
		previous, epoch = e.Hash, journalEpoch(e)
		s.trackRetentionEntry(e)
		if sequence == state.AppliedSequence && e.Hash != state.AppliedHash {
			return ErrCorrupt
		}
		if state.Role != "follower" && e.Blob != nil {
			info, err = os.Lstat(s.replicationPath("blobs/" + e.Blob.SHA256 + ".data"))
			if err != nil || !info.Mode().IsRegular() || info.Size() != e.Blob.Size {
				return ErrCorrupt
			}
		}
	}
	if previous != state.ReceivedHash {
		return ErrCorrupt
	}
	if err = s.validatePromotionPosition(); err != nil {
		return err
	}
	if err = s.validateDurabilityPosition(); err != nil {
		return err
	}
	if err = s.validateFollowerPositions(); err != nil {
		return err
	}
	if err = s.loadRetentionReceiptPins(context.Background()); err != nil {
		return err
	}
	if err = s.resumeCompaction(context.Background(), nil); err != nil {
		return err
	}
	if err = s.upgradeReplicationAuthority(); err != nil {
		return err
	}
	return syncDirectory(s.replicationPath(""))
}

//nolint:contextcheck // Startup owns the process lock and must validate durable state before the context-free Open API returns.
func (s *Store) validateReplicationFiles() error {
	count := 0
	s.journalUsage = JournalUsage{}
	return walkBackupTree(context.Background(), s.replicationPath(""), "", 0, func(path string, info os.FileInfo) error {
		count++
		if count > 3*api.ObjectNodeReplicationMaxEntries {
			return ErrCapacity
		}
		full := "replication/" + path
		name := filepath.Base(path)
		if !info.IsDir() && strings.HasPrefix(name, ".tmp-") && validReceiptID(strings.TrimPrefix(name, ".tmp-")) {
			return os.Remove(s.replicationPath(path))
		}
		if strings.HasPrefix(path, "transfer/") && strings.HasSuffix(name, ".part") && validScrubFindingID(strings.TrimSuffix(name, ".part")) && s.replication.Role == "follower" {
			return nil
		}
		if !validReplicationBackupPath(full, info.IsDir()) {
			return ErrCorrupt
		}
		if strings.HasPrefix(path, "log/") {
			sequence, _ := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
			if sequence <= s.replication.BaseSequence && (s.replication.Checkpoint == nil || !s.replication.Checkpoint.Pruning) || sequence > s.replication.ReceivedSequence+1 || s.replication.Role == "fenced" && sequence > s.replication.ReceivedSequence {
				return ErrCorrupt
			}
		}
		return s.observeRetentionFile(path, info)
	})
}

//nolint:contextcheck // Publication is a synchronous durable boundary shared by context-free catalog APIs; Close waits for it.
func (s *Store) journalRecord(path string, value any) (bool, error) {
	if err := s.finishReplicationPending(); err != nil {
		return false, err
	}
	if s.replication.Role != "primary" {
		return false, ErrReplica
	}
	if s.replication.AppliedSequence >= api.ObjectNodeReplicationMaxSequence {
		return false, ErrCapacity
	}
	relative, err := filepath.Rel(s.root, path)
	if err != nil {
		return false, err
	}
	body, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	blob, err := replicationMetadata(filepath.ToSlash(relative), body)
	if err != nil {
		return false, err
	}
	if blob != nil {
		if err = s.installJournalBlob(context.Background(), filepath.Join(s.root, blob.Path), blob); err != nil {
			return false, err
		}
	}
	return s.appendJournal(JournalEntry{Path: filepath.ToSlash(relative), Data: body, Blob: blob})
}

func (s *Store) journalRemove(path string) (bool, error) {
	if err := s.finishReplicationPending(); err != nil {
		return false, err
	}
	if !validReplicationRemoval(path) {
		return false, ErrInvalid
	}
	return s.appendJournal(JournalEntry{Path: path, Remove: true})
}

func (s *Store) appendJournal(e JournalEntry) (bool, error) {
	if s.replication.Role != "primary" {
		return false, ErrReplica
	}
	if s.replication.AppliedSequence >= api.ObjectNodeReplicationMaxSequence {
		return false, ErrCapacity
	}
	e.Version, e.StreamID, e.Sequence, e.Previous, e.CreatedAt, e.Epoch = 2, s.replication.StreamID, s.replication.AppliedSequence+1, s.replication.AppliedHash, time.Now().UTC(), s.replication.Epoch
	e = sealJournal(e)
	if !validJournalEntry(e) {
		return false, ErrInvalid
	}
	if err := backupSpace(s.replicationPath("log"), api.ObjectNodeReplicationRecordMaxBytes); err != nil {
		return false, err
	}
	s.replicationDirty = true
	published, err := s.persistJournalEntry(e)
	if err != nil {
		if !published {
			s.replicationDirty = false
		}
		return published, err
	}
	// A durable journal entry is a commit decision, even when publication or
	// its checkpoint acknowledgement is lost. Callers must retain new bytes.
	return true, s.finishReplicationPending()
}

// Adopt a last entry whose publication preceded its received checkpoint.
func (s *Store) adoptReplicationRecord() error {
	state, err := s.readReplicationState()
	if err != nil {
		return err
	}
	s.replication = &state
	if err = syncDirectory(s.replicationPath("")); err != nil {
		return err
	}
	if state.ReceivedSequence != state.AppliedSequence {
		return nil
	}
	if state.ReceivedSequence == api.ObjectNodeReplicationMaxSequence {
		return nil
	}
	e, err := s.readJournal(state.ReceivedSequence + 1)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.Previous != state.ReceivedHash {
		return ErrReplicationFork
	}
	if err = syncDirectory(s.replicationPath("log")); err != nil {
		return err
	}
	s.trackRetentionEntry(e)
	state.ReceivedSequence, state.ReceivedHash = e.Sequence, e.Hash
	return s.saveReplicationState(state)
}

//nolint:contextcheck // A durable commit decision must finish even when its originating request is cancelled.
func (s *Store) finishReplicationPending() error {
	if s.authorityDirty || s.replication != nil && s.replication.Role == "fenced" {
		return ErrFenced
	}
	if s.replication == nil {
		return nil
	}
	if s.replication.Role != "primary" {
		return ErrReplica
	}
	if err := s.adoptReplicationRecord(); err != nil {
		s.replicationDirty = true
		return err
	}
	if s.replication.ReceivedSequence == s.replication.AppliedSequence {
		s.replicationDirty = false
		return nil
	}
	s.replicationDirty = true
	e, err := s.readJournal(s.replication.ReceivedSequence)
	if err != nil {
		return err
	}
	if e.Previous != s.replication.AppliedHash {
		return ErrReplicationFork
	}
	if err = s.applyJournal(context.Background(), e); err != nil {
		return fmt.Errorf("object replication: replay: %w", err)
	}
	state := *s.replication
	state.AppliedSequence, state.AppliedHash = e.Sequence, e.Hash
	if err = s.saveReplicationState(state); err != nil {
		return err
	}
	s.replicationDirty = false
	return nil
}

// EnableReplication is an explicit offline format upgrade. Journal retention
// starts here; backups made afterwards contain its exact stream position.
func EnableReplication(ctx context.Context, directory string) (ReplicationStatus, error) {
	directory, err := backupDirectory(directory)
	if err != nil {
		return ReplicationStatus{}, err
	}
	s, err := openBackupSource(ctx, directory)
	if err != nil {
		return ReplicationStatus{}, err
	}
	defer func() { _ = s.Close() }()
	if s.replication != nil {
		return s.replication.ReplicationStatus, nil
	}
	stage := filepath.Join(s.root, ".replication-init-"+uuid.NewString())
	if err = os.Mkdir(stage, 0o700); err != nil {
		return ReplicationStatus{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	prepared := &Store{root: stage, replicationPersist: writeReplicationJSON}
	if err = os.Mkdir(prepared.replicationPath(""), 0o700); err != nil {
		return ReplicationStatus{}, err
	}
	for _, dir := range []string{"log", "blobs", "transfer"} {
		if err = os.Mkdir(prepared.replicationPath(dir), 0o700); err != nil {
			return ReplicationStatus{}, err
		}
	}
	stream := uuid.NewString()
	genesis := replicationGenesis(stream)
	node := uuid.NewString()
	state := replicationState{ReplicationStatus: ReplicationStatus{Version: 2, Role: "primary", StreamID: stream, BaseHash: genesis, ReceivedHash: genesis, AppliedHash: genesis, NodeID: node, PrimaryID: node, Epoch: 1}}
	if err = prepared.saveReplicationState(state); err != nil {
		return ReplicationStatus{}, err
	}
	if err = syncBackupTree(ctx, stage); err != nil {
		return ReplicationStatus{}, err
	}
	if err = os.Rename(prepared.replicationPath(""), s.replicationPath("")); err != nil {
		return ReplicationStatus{}, err
	}
	s.replication = prepared.replication
	if err = syncDirectory(s.root); err != nil {
		return ReplicationStatus{}, err
	}
	if err = writeReplicationFormat(s.root); err != nil {
		return ReplicationStatus{}, err
	}
	return s.replication.ReplicationStatus, nil
}
