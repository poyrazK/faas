package objectnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Store) installJournalBlob(ctx context.Context, source string, blob *ReplicationBlob) error {
	if err := s.journalParents(blob.Path); err != nil {
		return err
	}
	if prepared := s.preparedFor(blob); prepared != nil {
		return prepared.install()
	}
	destination := s.replicationPath("blobs/" + blob.SHA256 + ".data")
	_, before := os.Lstat(destination)
	if errors.Is(before, os.ErrNotExist) {
		if err := s.canRetainBlob(blob.Size); err != nil {
			return err
		}
	} else if before != nil {
		return before
	}
	err := installReplicationFile(ctx, source, destination, blob.Size, blob.SHA256)
	if errors.Is(before, os.ErrNotExist) {
		if info, statErr := os.Lstat(destination); statErr == nil && info.Mode().IsRegular() {
			s.journalUsage.Blobs++
			s.journalUsage.BlobBytes += info.Size()
		}
	}
	return err
}

func installReplicationFile(ctx context.Context, source, destination string, size int64, expected string) error {
	return installReplicationFileBudget(ctx, source, destination, size, expected, nil)
}

func installReplicationFileBudget(ctx context.Context, source, destination string, size int64, expected string, work *replicaWorkBudget) error {
	buffer := make([]byte, api.ObjectNodeBackupBufferBytes)
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() || info.Size() != size {
			return ErrCorrupt
		}
		digest, err := copyBackupFileBudget(ctx, destination, "", size, buffer, work)
		if err != nil {
			return err
		}
		if digest != expected {
			return ErrDigest
		}
		return syncDirectory(filepath.Dir(destination))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary := filepath.Join(filepath.Dir(destination), ".tmp-"+uuid.NewString())
	defer func() { _ = os.Remove(temporary) }()
	digest, err := copyBackupFileBudget(ctx, source, temporary, size, buffer, work)
	if err != nil {
		return err
	}
	if digest != expected {
		return ErrDigest
	}
	if err = renameNoReplace(temporary, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

// Make only vetted catalog parents. Every existing path component must be a
// real directory, including after an interrupted follower apply.
func (s *Store) journalParents(relative string) error {
	parent := filepath.Dir(relative)
	path := s.root
	for _, component := range strings.Split(parent, string(filepath.Separator)) {
		path = filepath.Join(path, component)
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return ErrCorrupt
		}
		if err = syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
		if err = syncDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyJournal(ctx context.Context, e JournalEntry) error {
	if err := s.applyJournalCatalog(ctx, e); err != nil {
		return err
	}
	// A projection failure pauses cleanup, never an already durable source write.
	s.observeRecoveryJournal(e)
	s.observeCatalogPath(filepath.Join(s.root, e.Path), e.Remove)
	return nil
}

func (s *Store) applyJournalCatalog(ctx context.Context, e JournalEntry) error {
	if !validJournalEntry(e) {
		return ErrCorrupt
	}
	if e.Path == "recovery.json" {
		var r RecoveryRetention
		if err := decodeBackupRecord(e.Data, &r); err != nil {
			return err
		}
		return s.applyRecovery(r)
	}
	path := filepath.Join(s.root, e.Path)
	if e.Remove {
		if err := s.checkRemovalParents(e.Path); err != nil {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		// A replayed ancestor deletion may have already removed the parent.
		if _, err := os.Lstat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return syncDirectory(filepath.Dir(path))
	}
	if err := s.journalParents(e.Path); err != nil {
		return err
	}
	if blob := e.Blob; blob != nil {
		if err := s.journalParents(blob.Path); err != nil {
			return err
		}
		if prepared := s.preparedFor(blob); prepared != nil {
			if err := prepared.install(); err != nil {
				return err
			}
		} else {
			work := s.replicaWork
			if s.replication == nil || s.replication.Role != "follower" {
				work = nil
			}
			if err := installReplicationFileBudget(ctx, s.replicationPath("blobs/"+blob.SHA256+".data"), filepath.Join(s.root, blob.Path), blob.Size, blob.SHA256, work); err != nil {
				return err
			}
		}
	}
	if strings.HasSuffix(e.Path, "/bucket.json") || strings.HasSuffix(e.Path, "/session.json") {
		child := "objects"
		if strings.HasPrefix(e.Path, "multipart/") {
			child = "parts"
		}
		if err := s.journalParents(filepath.ToSlash(filepath.Join(filepath.Dir(e.Path), child, "placeholder"))); err != nil {
			return err
		}
	}
	if _, err := s.persist(path, e.Data); err != nil {
		return err
	}
	if strings.HasPrefix(e.Path, "receipts/") && s.retentionIndex != nil {
		var r writeReceipt
		if err := decodeBackupRecord(e.Data, &r); err != nil {
			return err
		}
		s.retentionIndex.receipt(r)
	}
	if strings.HasSuffix(e.Path, "/current.json") && e.Blob != nil {
		if err := visitDirectory(filepath.Dir(path), func(file os.DirEntry) error {
			if strings.HasSuffix(file.Name(), ".data") && file.Name() != filepath.Base(e.Blob.Path) {
				return os.Remove(filepath.Join(filepath.Dir(path), file.Name()))
			}
			return nil
		}); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(path))
	}
	return nil
}

func (s *Store) checkRemovalParents(relative string) error {
	path := s.root
	for _, component := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
		path = filepath.Join(path, component)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return ErrCorrupt
		}
	}
	if info, err := os.Lstat(filepath.Join(s.root, relative)); err != nil && !errors.Is(err, os.ErrNotExist) || err == nil && !info.IsDir() {
		return ErrCorrupt
	}
	return nil
}
