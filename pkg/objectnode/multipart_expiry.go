package objectnode

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	bolt "go.etcd.io/bbolt"
)

type MultipartExpiryOptions struct {
	MaxSessions int
	MaxParts    int
	StepTime    time.Duration
}

func DefaultMultipartExpiryOptions() MultipartExpiryOptions {
	return MultipartExpiryOptions{api.ObjectNodeMultipartExpirySessions, api.ObjectNodeMultipartExpiryParts, api.ObjectNodeMultipartExpiryStepTime}
}

func (o MultipartExpiryOptions) Validate() error {
	if o.MaxSessions < 1 || o.MaxSessions > api.ObjectNodeMultipartExpiryMaxSessions || o.MaxParts < 1 || o.MaxParts > api.ObjectNodeMultipartExpiryMaxParts || o.StepTime <= 0 || o.StepTime > api.ObjectNodeMultipartExpiryMaxStepTime {
		return ErrInvalid
	}
	return nil
}

func (s *Store) indexedActiveMultipartCount(bucket, generation string) (int, error) {
	if err := s.requireCatalogIndex(); err != nil {
		return 0, err
	}
	var count uint64
	err := s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		var err error
		count, err = catalogCount(tx, catalogScope(nameHash(bucket), generation))
		return err
	})
	if err != nil || count > uint64(api.MaxActiveMultipartUploadsPerBucket) {
		s.failCatalogIndex()
		return 0, ErrCatalogIndex
	}
	return int(count), nil
}

// ExpireMultipart uses the persistent due queue. Its return value counts newly
// expired sessions; terminal tombstones remain for stable-identity recovery.
func (s *Store) ExpireMultipart(ctx context.Context, now time.Time) (int, error) {
	return s.ExpireMultipartStep(ctx, now, DefaultMultipartExpiryOptions())
}

// ExpireMultipartStep releases mu between candidate turns and never performs
// source payload replay. The wall-time budget is soft across durable decisions.
func (s *Store) ExpireMultipartStep(ctx context.Context, now time.Time, options MultipartExpiryOptions) (int, error) {
	if err := options.Validate(); err != nil || now.IsZero() {
		return 0, ErrInvalid
	}
	s.life.RLock()
	defer s.life.RUnlock()
	deadline := time.Now().Add(options.StepTime)
	count, parts := 0, 0
	for examined := 0; examined < options.MaxSessions && parts < options.MaxParts; examined++ {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		if examined > 0 && !time.Now().Before(deadline) {
			break
		}
		more, expired, removed, err := s.expireMultipartOne(ctx, now)
		count += expired
		parts += removed
		if err != nil || !more {
			return count, err
		}
	}
	return count, nil
}

func (s *Store) expireMultipartOne(ctx context.Context, now time.Time) (bool, int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, 0, 0, err
	}
	if err := s.servingError(); err != nil {
		return false, 0, 0, err
	}
	s.catalogStats.ExpirySteps++
	if err := s.requireCatalogIndex(); err != nil {
		return false, 0, 0, err
	}
	if err := s.expirySourceReady(); err != nil {
		s.catalogStats.ExpiryDeferred++
		return false, 0, 0, err
	}
	id, err := s.firstMultipartDue(now)
	if err != nil {
		s.failCatalogIndex()
		return false, 0, 0, ErrCatalogIndex
	}
	if id == "" {
		return false, 0, 0, nil
	}
	s.catalogStats.ExpiryExamined++
	u, err := s.readMultipart(id)
	if errors.Is(err, ErrNoUpload) {
		// A vanished terminal tombstone is only a stale scheduling hint.
		return true, 0, 0, s.changeExpiryHint(id, nil)
	}
	if err != nil {
		return false, 0, 0, err
	}
	if s.assembling[id] {
		s.catalogStats.ExpiryDeferred++
		return true, 0, 0, s.deferMultipartExpiry(u, now)
	}
	terminal, err := s.settleMultipartStatus(&u)
	if err != nil {
		return false, 0, 0, err
	}
	count := 0
	if !terminal && (u.Status == multipartActive || u.Status == multipartCompleting) {
		if now.Before(u.ExpiresAt) {
			return true, 0, 0, s.changeExpiryHint(id, catalogUploadHint(u, true))
		}
		b, err := s.bucket(u.Bucket)
		if err != nil || b.Generation != u.Generation {
			return false, 0, 0, ErrCorrupt
		}
		u.Status = multipartExpired
		if err = s.writeMultipart(u); err != nil {
			return false, 0, 0, err
		}
		count = 1
		s.catalogStats.Expired++
	}
	removed, err := s.cleanMultipartPartStep(u.ID)
	if removed {
		s.catalogStats.PartsRemoved++
	}
	if err == nil {
		err = s.requireCatalogIndex()
	} else if errors.Is(err, ErrCatalogBusy) {
		s.catalogStats.ExpiryDeferred++
		if deferErr := s.deferMultipartExpiry(u, now); deferErr != nil {
			err = errors.Join(err, deferErr)
		}
	}
	return true, count, boolInt(removed), err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) expirySourceReady() error {
	if s.pending != nil || s.replicationDirty || s.replication != nil && s.replication.ReceivedSequence != s.replication.AppliedSequence {
		return ErrCatalogBusy
	}
	if s.replication != nil {
		if err := s.adoptReplicationRecord(); err != nil {
			return err
		}
		if s.replication.ReceivedSequence != s.replication.AppliedSequence {
			return ErrCatalogBusy
		}
	}
	return nil
}

func (s *Store) firstMultipartDue(now time.Time) (string, error) {
	id := ""
	err := s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(catalogDue))
		if bucket == nil {
			return ErrCatalogIndex
		}
		key, value := bucket.Cursor().First()
		if key == nil {
			return nil
		}
		if len(key) != 8+36 || !validReceiptID(string(value)) || string(key[8:]) != string(value) {
			return ErrCorrupt
		}
		if binary.BigEndian.Uint64(key[:8]) <= uint64(now.UnixMilli()) {
			id = string(value)
		}
		return nil
	})
	return id, err
}

func (s *Store) changeExpiryHint(id string, next *catalogUpload) error {
	if err := s.requireCatalogIndex(); err != nil {
		return err
	}
	if err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error { return catalogPutUpload(tx, id, next) }); err != nil {
		s.failCatalogIndex()
		return ErrCatalogIndex
	}
	return nil
}

func (s *Store) deferMultipartExpiry(u MultipartUpload, now time.Time) error {
	next := catalogUploadHint(u, true)
	next.Due = now.Add(api.ObjectMultipartCleanupRetry).UnixMilli()
	return s.changeExpiryHint(u.ID, next)
}

// Read one part directory and cap its child count before recursive physical
// cleanup. Unexpected trees or excessive orphan files require offline recovery.
func (s *Store) cleanMultipartPartStep(id string) (bool, error) {
	parts := filepath.Join(s.multipartPath(id), "parts")
	entries, err := readDirectoryPrefix(parts, 1)
	if errors.Is(err, os.ErrNotExist) {
		return false, s.refreshExpiryAfterCleanup(id)
	}
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, s.removeExpiryDirectory(parts)
	}
	e := entries[0]
	n, err := strconv.ParseInt(e.Name(), 10, 32)
	if err != nil || n < 1 || n > api.MaxMultipartParts || strconv.FormatInt(n, 10) != e.Name() || !e.IsDir() {
		return false, ErrCorrupt
	}
	path := filepath.Join(parts, e.Name())
	files, err := readDirectoryPrefix(path, api.ObjectNodeMultipartExpiryPartFiles+1)
	if err != nil {
		return false, err
	}
	if len(files) > api.ObjectNodeMultipartExpiryPartFiles {
		return false, ErrCatalogBusy
	}
	for _, file := range files {
		if !file.Type().IsRegular() || file.Name() != "current.json" && (!strings.HasSuffix(file.Name(), ".data") || !validReceiptID(strings.TrimSuffix(file.Name(), ".data"))) {
			return false, ErrCorrupt
		}
	}
	if err = s.removeExpiryDirectory(path); err != nil {
		return false, err
	}
	if s.catalogCheckpoint != nil {
		if err = s.catalogCheckpoint("expiry part removed"); err != nil {
			return true, err
		}
	}
	entries, err = readDirectoryPrefix(parts, 1)
	if err == nil && len(entries) == 0 {
		err = s.removeExpiryDirectory(parts)
	}
	return true, err
}

func readDirectoryPrefix(path string, limit int) ([]os.DirEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrCorrupt
	}
	f, err := os.Open(path) //nolint:forbidigo // Generated private catalog directory.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(limit)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, err
}

func (s *Store) removeExpiryDirectory(path string) error {
	if s.replication != nil && filepath.Base(path) == "parts" {
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		_, err = s.journalRemove(filepath.ToSlash(rel))
		return err
	}
	// A durable terminal session already made these part bytes unreachable.
	// Reclaim individual parts locally; publish the existing whole-parts removal
	// only once empty. Followers may retain terminal bytes until that final entry.
	s.invalidateInventory()
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	s.observeCatalogPath(path, true)
	return nil
}

func (s *Store) refreshExpiryAfterCleanup(id string) error {
	u, err := s.readMultipart(id)
	if err != nil {
		return err
	}
	return s.changeExpiryHint(id, catalogUploadHint(u, false))
}
