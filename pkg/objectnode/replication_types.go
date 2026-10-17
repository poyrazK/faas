package objectnode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
)

const ReplicationQuery = "gregale-replication"

// ReplicationStatus describes physical journal positions, not a quorum commit.
// Followers never infer a write outcome from their position alone.
type ReplicationStatus struct {
	Version          int                  `json:"version"`
	Role             string               `json:"role"`
	StreamID         string               `json:"stream_id"`
	BaseSequence     int64                `json:"base_sequence"`
	BaseHash         string               `json:"base_hash"`
	ReceivedSequence int64                `json:"received_sequence"`
	ReceivedHash     string               `json:"received_hash"`
	AppliedSequence  int64                `json:"applied_sequence"`
	AppliedHash      string               `json:"applied_hash"`
	UpdatedAt        time.Time            `json:"updated_at"`
	NodeID           string               `json:"node_id,omitempty"`
	PrimaryID        string               `json:"primary_id,omitempty"`
	Epoch            int64                `json:"epoch,omitempty"`
	Promotion        *PrimaryHandoff      `json:"promotion,omitempty"`
	Handoff          *PrimaryHandoff      `json:"handoff,omitempty"`
	Durability       *ReplicaDurability   `json:"durability,omitempty"`
	Checkpoint       *JournalCheckpoint   `json:"checkpoint,omitempty"`
	Followers        []RegisteredFollower `json:"followers,omitempty"`
}

type replicationState struct {
	ReplicationStatus
	Seal string `json:"seal"`
}

type ReplicationBlob struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// JournalEntry is one durable physical catalog mutation. Receipt intent,
// publication and terminal proof remain distinct ordered mutations.
type JournalEntry struct {
	Version   int              `json:"version"`
	StreamID  string           `json:"stream_id"`
	Sequence  int64            `json:"sequence"`
	Previous  string           `json:"previous"`
	CreatedAt time.Time        `json:"created_at"`
	Path      string           `json:"path"`
	Remove    bool             `json:"remove"`
	Data      json.RawMessage  `json:"data"`
	Blob      *ReplicationBlob `json:"blob"`
	Hash      string           `json:"hash"`
	Epoch     int64            `json:"epoch,omitempty"`
}

func (s *Store) replicationPath(name string) string {
	return filepath.Join(s.root, "replication", name)
}
func journalName(sequence int64) string { return fmtSequence(sequence) + ".json" }
func fmtSequence(sequence int64) string {
	value := strconv.FormatInt(sequence, 10)
	return strings.Repeat("0", 20-len(value)) + value
}

func sealJournal(e JournalEntry) JournalEntry { e.Hash = ""; e.Hash = scrubSeal(e); return e }
func sealReplication(s replicationState) replicationState {
	s.Seal = ""
	s.Seal = scrubSeal(s)
	return s
}
func replicationGenesis(stream string) string { return nameHash("gregale-replication-v1:" + stream) }

func validReplicationStatus(s ReplicationStatus) bool {
	if s.Version < 1 || s.Version > 4 || s.Role != "primary" && s.Role != "follower" && s.Role != "fenced" || !validReceiptID(s.StreamID) || s.UpdatedAt.IsZero() || s.BaseSequence < 0 || s.AppliedSequence < s.BaseSequence || s.ReceivedSequence < s.AppliedSequence || s.ReceivedSequence > api.ObjectNodeReplicationMaxSequence || s.ReceivedSequence-s.AppliedSequence > 1 || s.ReceivedSequence-s.BaseSequence > api.ObjectNodeReplicationMaxEntries || !validScrubFindingID(s.BaseHash) || !validScrubFindingID(s.ReceivedHash) || !validScrubFindingID(s.AppliedHash) {
		return false
	}
	if s.AppliedSequence == s.BaseSequence && s.AppliedHash != s.BaseHash || s.AppliedSequence == s.ReceivedSequence && s.AppliedHash != s.ReceivedHash {
		return false
	}
	if s.BaseSequence == 0 && s.BaseHash != replicationGenesis(s.StreamID) {
		return false
	}
	return validReplicationAuthority(s) && validReplicaDurability(s) && validJournalRetention(s)
}

func validJournalEntry(e JournalEntry) bool {
	if e.Version != 1 && e.Version != 2 || e.Version == 1 && e.Epoch != 0 || e.Version == 2 && (e.Epoch < 1 || e.Epoch > api.ObjectNodeReplicationMaxEpoch) || !validReceiptID(e.StreamID) || e.Sequence < 1 || e.Sequence > api.ObjectNodeReplicationMaxSequence || e.CreatedAt.IsZero() || !validScrubFindingID(e.Previous) || e.Hash != sealJournal(e).Hash {
		return false
	}
	if e.Remove {
		return (e.Data == nil || string(e.Data) == "null") && e.Blob == nil && validReplicationRemoval(e.Path)
	}
	blob, err := replicationMetadata(e.Path, e.Data)
	return err == nil && (blob == nil && e.Blob == nil || blob != nil && e.Blob != nil && *blob == *e.Blob)
}

func validReplicationRemoval(path string) bool {
	if !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return false
	}
	p := strings.Split(path, "/")
	if len(p) >= 2 && p[0] == "buckets" && validScrubFindingID(p[1]) {
		return len(p) == 2 || len(p) == 4 && p[2] == "objects" && validScrubFindingID(p[3])
	}
	return len(p) == 3 && p[0] == "multipart" && validReceiptID(p[1]) && p[2] == "parts"
}

func replicationMetadata(path string, data []byte) (*ReplicationBlob, error) {
	if !validBackupPath(path, false) || len(data) > maxRecordBytes {
		return nil, ErrInvalid
	}
	p := strings.Split(path, "/")
	var o Object
	var blobPath string
	switch {
	case path == "recovery.json":
		var r RecoveryRetention
		if decodeBackupRecord(data, &r) != nil || !validRecovery(r) {
			return nil, ErrCorrupt
		}
		return nil, nil
	case len(p) == 3 && p[0] == "buckets" && p[2] == "bucket.json":
		var b bucketRecord
		if decodeBackupRecord(data, &b) != nil || b.Version != 1 || !ValidBucket(b.Name) || nameHash(b.Name) != p[1] || !validReceiptID(b.Generation) || b.CreatedAt.IsZero() {
			return nil, ErrCorrupt
		}
		return nil, nil
	case len(p) == 3 && p[0] == "multipart" && p[2] == "session.json":
		var u MultipartUpload
		if decodeBackupRecord(data, &u) != nil || u.Version != 1 || u.ID != p[1] || !validReceiptID(u.ReceiptID) || u.ID != objectmultipart.NativeUploadID(u.ReceiptID) || u.Seal != sealMultipart(u).Seal || !validMultipartSettlement(u) {
			return nil, ErrCorrupt
		}
		if !ValidBucket(u.Bucket) || !validReceiptID(u.Generation) || !validKey(u.Key) || u.CreatedAt.IsZero() || !u.ExpiresAt.After(u.CreatedAt) || !u.Checksum.Valid() || u.MaxBytes < 1 || u.MaxBytes > api.MaxObjectUploadBytes || u.Headers.Get(MultipartMetadataHeader) != u.ReceiptID {
			return nil, ErrCorrupt
		}
		switch u.Status {
		case multipartActive, multipartAborted, multipartExpired:
			if u.Status == multipartActive && u.Completion != nil {
				return nil, ErrCorrupt
			}
		case multipartCompleting, multipartCompleted, multipartRejected:
			if u.Completion == nil || u.Completion.UploadID != u.ID || !validManifest(*u.Completion, u.Checksum, u.MaxBytes) {
				return nil, ErrCorrupt
			}
		default:
			return nil, ErrCorrupt
		}
		return nil, nil
	case len(p) == 2 && p[0] == "receipts":
		var r writeReceipt
		if decodeBackupRecord(data, &r) != nil || r.Version != 1 || r.ID+".json" != p[1] || r.Seal != sealReceipt(r).Seal || r.RequestHash != receiptRequestHash(r) || !ValidBucket(r.Bucket) || !validReceiptID(r.Generation) || !validReplicationObject(r.Object) || r.Kind != "" && r.Kind != "multipart" || r.Kind == "multipart" && !validScrubFindingID(r.ManifestHash) || r.Status != receiptCommitting && r.Status != ReceiptCommitted && r.Status != ReceiptRejected || !validReceiptSettlement(r) {
			return nil, ErrCorrupt
		}
		if r.Status != receiptCommitting {
			return nil, nil
		}
		o = r.Object
		blobPath = filepath.ToSlash(filepath.Join("buckets", nameHash(r.Bucket), "objects", nameHash(o.Key), o.ID+".data"))
	case len(p) == 5 && p[4] == "current.json":
		if p[0] == "buckets" {
			if decodeBackupRecord(data, &o) != nil || nameHash(o.Key) != p[3] {
				return nil, ErrCorrupt
			}
		} else {
			var part MultipartPart
			if decodeBackupRecord(data, &part) != nil || part.Seal != sealPart(part).Seal || strconv.Itoa(int(part.Number)) != p[3] {
				return nil, ErrCorrupt
			}
			o = part.Object
		}
		blobPath = filepath.ToSlash(filepath.Join(filepath.Dir(path), o.ID+".data"))
	default:
		return nil, ErrInvalid
	}
	if !validReplicationObject(o) {
		return nil, ErrCorrupt
	}
	return &ReplicationBlob{Path: blobPath, Size: o.Size, SHA256: o.SHA256}, nil
}

func validReplicationObject(o Object) bool {
	return o.Version == 1 && validReceiptID(o.ID) && validKey(o.Key) && validScrubFindingID(o.SHA256) && o.Size >= 0 && o.Size <= api.MaxObjectUploadBytes && !o.Modified.IsZero() && validObjectETag(o)
}

func validReplicationBackupPath(path string, directory bool) bool {
	p := strings.Split(path, "/")
	if p[0] != "replication" {
		return false
	}
	if len(p) == 1 {
		return directory
	}
	if len(p) == 2 {
		return directory && (p[1] == "log" || p[1] == "blobs" || p[1] == "transfer") || !directory && p[1] == "state.json"
	}
	if len(p) != 3 || directory {
		return false
	}
	if p[1] == "log" {
		sequence, err := strconv.ParseInt(strings.TrimSuffix(p[2], ".json"), 10, 64)
		return err == nil && sequence > 0 && sequence <= api.ObjectNodeReplicationMaxSequence && journalName(sequence) == p[2]
	}
	return p[1] == "blobs" && strings.HasSuffix(p[2], ".data") && validScrubFindingID(strings.TrimSuffix(p[2], ".data"))
}

func (s *Store) ReplicationStatus() (ReplicationStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ReplicationStatus{}, ErrClosed
	}
	if s.replication == nil {
		return ReplicationStatus{}, ErrReplicationOff
	}
	if s.replicationDirty || s.authorityDirty {
		return ReplicationStatus{}, ErrCorrupt
	}
	return replicationSnapshot(s.replication.ReplicationStatus), nil
}

func (s *Store) ReplicationEntry(stream string, sequence int64) (JournalEntry, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkReplicationEntryPosition(stream, sequence); err != nil {
		return JournalEntry{}, err
	}
	return s.readJournal(sequence)
}

// The caller holds the catalog lock through reading the immutable entries.
func (s *Store) checkReplicationEntryPosition(stream string, sequence int64) error {
	if s.closed {
		return ErrClosed
	}
	if s.replication == nil || s.replication.Role == "follower" {
		return ErrReplicationOff
	}
	if s.replicationDirty || s.authorityDirty {
		return ErrCorrupt
	}
	if stream == s.replication.StreamID && sequence <= s.replication.BaseSequence && sequence > 0 {
		return ErrReplicaReseed
	}
	if stream != s.replication.StreamID || sequence < 1 || sequence > s.replication.AppliedSequence {
		return ErrReplicationFork
	}
	return nil
}

func (s *Store) OpenReplicationBlob(stream, digest string) (*os.File, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.replication == nil || s.replication.Role == "follower" {
		return nil, ErrReplicationOff
	}
	if stream != s.replication.StreamID || !validScrubFindingID(digest) {
		return nil, ErrReplicationFork
	}
	if s.replicationDirty || s.authorityDirty {
		return nil, ErrCorrupt
	}
	return openBackupFile(s.replicationPath("blobs/" + digest + ".data"))
}
