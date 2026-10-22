package objectnode

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

func ValidBucket(name string) bool {
	return objectinventory.ValidBucket(name)
}

func (s *Store) CreateBucket(bucket string) error {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if err := s.finishPending(false); err != nil {
		return err
	}
	if !ValidBucket(bucket) {
		return ErrInvalid
	}
	if _, err := s.bucket(bucket); !errors.Is(err, ErrNoBucket) {
		if err == nil {
			return syncDirectory(s.bucketPath(bucket))
		}
		return err
	}
	admitted, err := s.admitWriteLocked("bucket", 0, false)
	if err != nil {
		return err
	}
	defer s.releaseAdmissionLocked(admitted)
	dir := s.bucketPath(bucket)
	if err := os.MkdirAll(filepath.Join(dir, "objects"), 0o700); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(dir, "objects")); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(dir)); err != nil {
		return err
	}
	_, err = s.writeRecord(filepath.Join(dir, "bucket.json"), bucketRecord{Version: 1, Name: bucket, Generation: uuid.NewString(), CreatedAt: time.Now().UTC()})
	return err
}

func (s *Store) HeadBucket(bucket string) error {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	_, err := s.bucket(bucket)
	return err
}

func (s *Store) DeleteBucket(bucket string) error {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if err := s.finishPending(false); err != nil {
		return err
	}
	if _, err := s.bucket(bucket); err != nil {
		return err
	}
	b, err := s.bucket(bucket)
	if err != nil {
		return err
	}
	active, err := s.activeMultipartCount(bucket, b.Generation)
	if err != nil {
		return err
	}
	if active != 0 {
		return ErrNotEmpty
	}
	dir := s.bucketPath(bucket)
	err = visitDirectory(filepath.Join(dir, "objects"), func(entry os.DirEntry) error {
		_, err := readObject(filepath.Join(dir, "objects", entry.Name()))
		if errors.Is(err, ErrNoObject) {
			return nil
		}
		if err != nil {
			return err
		}
		return ErrNotEmpty
	})
	if err != nil {
		return err
	}
	if s.replication != nil {
		s.invalidateInventory()
		_, err = s.journalRemove(filepath.ToSlash(filepath.Join("buckets", nameHash(bucket))))
		return err
	}
	trash := filepath.Join(s.root, "trash", uuid.NewString())
	s.invalidateInventory()
	if err = os.Rename(dir, trash); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(dir)); err != nil {
		s.failCatalogIndex()
		return err
	}
	s.observeCatalogPath(dir, true)
	if err = syncDirectory(filepath.Dir(trash)); err != nil {
		return err
	}
	// The catalog deletion is already durable. Startup retries physical cleanup.
	_ = os.RemoveAll(trash)
	return nil
}
