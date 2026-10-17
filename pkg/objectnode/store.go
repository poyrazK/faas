// Package objectnode implements the first, single-process Gregale object node.
// Its on-disk catalog and immutable blobs are private to one locked data directory.
package objectnode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

var (
	ErrNoBucket         = errors.New("object node: bucket not found")
	ErrNoObject         = errors.New("object node: object not found")
	ErrNotEmpty         = errors.New("object node: bucket is not empty")
	ErrInvalid          = errors.New("object node: invalid request")
	ErrPrecondition     = errors.New("object node: precondition failed")
	ErrDigest           = errors.New("object node: checksum mismatch")
	ErrCorrupt          = errors.New("object node: corrupt catalog")
	ErrClosed           = errors.New("object node: store is closed")
	ErrNoReceipt        = errors.New("object node: write receipt not found")
	ErrReceiptReuse     = errors.New("object node: write receipt belongs to another request")
	ErrReceiptExpired   = errors.New("object node: write receipt is outside the first-use window")
	ErrNoUpload         = errors.New("object node: multipart upload not found")
	ErrInvalidPart      = errors.New("object node: multipart part does not match")
	ErrPartOrder        = errors.New("object node: invalid multipart part order")
	ErrPartSmall        = errors.New("object node: non-final multipart part is too small")
	ErrUploadBusy       = errors.New("object node: multipart completion is in progress")
	ErrCapacity         = errors.New("object node: multipart limit exceeded")
	ErrInventoryChanged = errors.New("object node: inventory revision changed")
	ErrScrubBusy        = errors.New("object node: scrub operation in progress")
	ErrReplica          = errors.New("object node: follower cannot serve or accept local mutations")
	ErrReplicationOff   = errors.New("object node: replication is not enabled")
	ErrReplicationFork  = errors.New("object node: replication stream or position does not match")
	ErrFenced           = errors.New("object node: primary write authority has been fenced")
	ErrReplicaPending   = errors.New("object node: replica durability is not confirmed")
	ErrReplicaReseed    = errors.New("object node: retained history is unavailable; reseed the follower from a fresh verified backup")
	ErrRetentionPinned  = errors.New("object node: compaction is pinned by a follower or replica confirmation")
	ErrRecoveryIndex    = errors.New("object node: recovery cleanup index requires an offline rebuild")
	ErrRecoveryBusy     = errors.New("object node: recovery cleanup deferred by pending catalog work")
	ErrCatalogIndex     = errors.New("object node: catalog index unavailable; restart to rebuild")
	ErrCatalogBusy      = errors.New("object node: catalog maintenance deferred by pending work")
	ErrUsageAudit       = errors.New("object node: usage counters require a fresh healthy audit")
)

const maxRecordBytes = 1 << 20 // On-disk format budget, not a customer quota.

type bucketRecord struct {
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	Generation string    `json:"generation"`
	CreatedAt  time.Time `json:"created_at"`
}

// Store serializes catalog publication while streaming uploads outside that
// critical section. A process lock prevents independent writers sharing a disk.
type Store struct {
	root                 string
	lock                 *flock.Flock
	life                 sync.RWMutex
	mu                   sync.RWMutex
	closed               bool
	pending              *writeReceipt
	persist              func(string, any) (bool, error)
	assembling           map[string]bool
	inventoryBoot        string
	inventoryEpoch       uint64
	scrubMu              sync.Mutex
	scrubPersist         func(string, any) (bool, error)
	replication          *replicationState
	replicationDirty     bool
	authorityDirty       bool
	replicationPersist   func(string, any) (bool, error)
	replicationChanged   chan struct{}
	journalUsage         JournalUsage
	replaySlots          chan struct{}
	preparedReplay       *preparedReplay
	replayCopy           func(context.Context, string, string, int64, []byte) (string, error)
	replicaWork          *replicaWorkBudget
	retentionIndex       *journalRetentionIndex
	compaction           *onlineCompaction
	compactionStats      CompactionStatus
	compactionCheckpoint func(string) error
	compactionSync       func(string) error
	admission            *writeAdmission
	recoveryClock        func() time.Time
	recoveryCheckpoint   func(string) error
	cleanupIndex         *recoveryCleanupIndex
	cleanupStats         RecoveryCleanupStatus
	cleanupCheckpoint    func(string) error
	cleanupIndexFault    func() error
	catalogIndex         *catalogIndex
	catalogStats         CatalogIndexStatus
	catalogIndexFault    func() error
	catalogCheckpoint    func(string) error
	catalogListExamined  atomic.Uint64
	catalogReadFailures  atomic.Uint64
	usageTotals          objectinventory.Totals
	usageAudit           atomic.Pointer[usageAuditState]
	usageQueries         atomic.Uint64
	usageAuditFailures   atomic.Uint64
	auditMu              sync.Mutex
	auditStats           InventoryAuditStatus
	auditProof           string
	auditCheckpoint      func() error
	auditKey             [32]byte
	auditSourceRoot      auditSet
	auditTrackedEpoch    uint64
	auditMetaProof       string
	auditPending         atomic.Int64
	auditOverflows       atomic.Uint64
	auditBacklogSince    atomic.Int64
}

// Open requires an existing parent directory on a local Linux filesystem with
// atomic rename and working file/directory fsync. It fails closed on bad metadata.
func Open(directory string) (*Store, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("object node: resolve data directory: %w", err)
	}
	if err = os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("object node: create data directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("object node: data directory must be a real directory private to its owner")
	}
	s, err := lockStore(root)
	if err != nil {
		return nil, err
	}
	if err = s.initialize(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("object node: initialize data directory: %w", err)
	}
	return s, nil
}

func lockStore(root string) (*Store, error) {
	if info, err := os.Lstat(filepath.Join(root, ".lock")); err != nil && !errors.Is(err, os.ErrNotExist) || err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("object node: invalid process lock file")
	}
	s := &Store{root: root, lock: flock.New(filepath.Join(root, ".lock")), persist: writeJSON, assembling: make(map[string]bool), inventoryBoot: uuid.NewString(), scrubPersist: writeJSON, replicationPersist: writeReplicationJSON, replicationChanged: make(chan struct{})}
	s.replaySlots = make(chan struct{}, api.ObjectNodeReplayPreparationConcurrency)
	s.replayCopy = copyBackupFile
	locked, err := s.lock.TryLock()
	if err != nil || !locked {
		_ = s.lock.Close()
		return nil, errors.New("object node: data directory is already locked or unavailable")
	}
	return s, nil
}

func (s *Store) initialize() error {
	if err := s.checkFormat(); err != nil {
		return err
	}
	if err := s.loadReplication(false); err != nil {
		return err
	}
	if s.replication != nil && s.replication.Role == "fenced" {
		return s.validateRecoveryFormat() // Export the sealed stream only; never run primary recovery.
	}
	if err := s.finishReplicationPending(); err != nil {
		return err
	}
	if err := s.validateRecoveryFormat(); err != nil {
		return err
	}
	for _, name := range []string{"buckets", "staging", "trash", "receipts", "multipart", "scrub"} {
		if err := os.Mkdir(filepath.Join(s.root, name), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	if err := syncDirectory(s.root); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(s.root)); err != nil {
		return err
	}
	if err := s.recoverReceipts(); err != nil {
		return err
	}
	if err := s.recoverMultipart(); err != nil {
		return err
	}
	if err := s.recover(); err != nil {
		return err
	}
	if err := s.recoverScrub(); err != nil {
		return err
	}
	s.startRecoveryIndex()
	s.startCatalogIndex()
	return nil
}

func (s *Store) checkFormat() error {
	type diskFormat struct {
		Engine  string `json:"engine"`
		Version int    `json:"version"`
	}
	path := filepath.Join(s.root, "format.json")
	var format diskFormat
	err := readJSON(path, &format)
	if err == nil {
		if format.Engine != "gregale-object-node" || format.Version < 1 || format.Version > 5 {
			return ErrCorrupt
		}
		if format.Version < 3 {
			_, err = writeJSON(path, diskFormat{Engine: format.Engine, Version: 3})
			return err
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ErrCorrupt
	}
	if err = visitDirectory(s.root, func(entry os.DirEntry) error {
		if entry.Name() != ".lock" {
			return errors.New("object node: refusing an unrecognized nonempty data directory")
		}
		return nil
	}); err != nil {
		return err
	}
	_, err = writeJSON(path, diskFormat{Engine: "gregale-object-node", Version: 3})
	return err
}

// Close waits for in-flight mutations before releasing the exclusive disk lock.
func (s *Store) Close() error {
	s.life.Lock()
	defer s.life.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.compaction != nil {
		s.compaction.close()
	}
	s.signalReplicationChange()
	return errors.Join(s.closeCatalogIndex(), s.closeRecoveryIndex(), s.lock.Close())
}

func nameHash(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func (s *Store) bucketPath(bucket string) string {
	return filepath.Join(s.root, "buckets", nameHash(bucket))
}

func (s *Store) objectPath(bucket, key string) string {
	return filepath.Join(s.bucketPath(bucket), "objects", nameHash(key))
}

// Called under the catalog write lock, including before uncertain publication.
func (s *Store) invalidateInventory() {
	s.inventoryEpoch++
	if s.inventoryEpoch == 0 {
		s.inventoryBoot = uuid.NewString()
	}
}

func (s *Store) writeRecord(path string, value any) (bool, error) {
	s.invalidateInventory()
	if s.replication != nil {
		return s.journalRecord(path, value)
	}
	published, err := s.persist(path, value)
	if err == nil {
		s.observeCatalogPath(path, false)
	} else {
		s.failCatalogIndex()
	}
	return published, err
}

func (s *Store) bucket(bucket string) (bucketRecord, error) {
	var b bucketRecord
	if s.authorityDirty || s.replication != nil && s.replication.Role == "fenced" {
		return b, ErrFenced
	}
	if s.replicationDirty {
		return b, ErrCorrupt
	}
	err := readJSON(filepath.Join(s.bucketPath(bucket), "bucket.json"), &b)
	if errors.Is(err, os.ErrNotExist) {
		return b, ErrNoBucket
	}
	if err != nil || b.Version != 1 || b.Name != bucket || uuid.Validate(b.Generation) != nil {
		return b, ErrCorrupt
	}
	return b, nil
}

func readJSON(path string, value any) error {
	f, err := os.Open(path) //nolint:forbidigo // Generated path under the exclusively owned data directory.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxRecordBytes {
		return ErrCorrupt
	}
	dec := json.NewDecoder(io.LimitReader(f, maxRecordBytes))
	dec.DisallowUnknownFields()
	if err = dec.Decode(value); err != nil {
		return ErrCorrupt
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return ErrCorrupt
	}
	return nil
}

// writeJSON reports publication separately: after rename, a failed directory
// fsync is uncertain and the caller must retain the newly referenced blob.
func writeJSON(path string, value any) (published bool, err error) {
	return writeJSONLimit(path, value, maxRecordBytes)
}

func writeJSONLimit(path string, value any, limit int) (published bool, err error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > limit {
		return false, ErrInvalid
	}
	temporary := filepath.Join(filepath.Dir(path), ".tmp-"+uuid.NewString())
	f, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(temporary) }()
	_, err = f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err = os.Rename(temporary, path); err != nil {
		return false, err
	}
	return true, syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path) //nolint:forbidigo // Trusted daemon-owned directory; fsync makes publication durable.
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

// visitDirectory keeps startup and listing working memory bounded.
func visitDirectory(path string, visit func(os.DirEntry) error) error {
	dir, err := os.Open(path) //nolint:forbidigo // Generated path in the private data directory.
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	for {
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if err = visit(entry); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
