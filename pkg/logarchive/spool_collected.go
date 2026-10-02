package logarchive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/region"
)

const (
	spoolCollectedSuffix = ".jsonl.collected"
	spoolCollectedMagic  = "gregale-collected-v1\n"
)

// ErrSpoolDayCollected rejects delayed writes to a day whose archive was
// durably collected. It is an expected retention outcome, not a disk failure.
var ErrSpoolDayCollected = errors.New("logarchive: spool day already collected")

type localCollectionResult struct {
	Files        int
	PendingBytes int64
}

func (s *Spool) collectionBase(instance, day string) (string, error) {
	if instance == "" || instance == "." || instance == ".." || len(instance) > MaxInstanceIDLen || strings.ContainsAny(instance, "/\\\x00") || !region.ValidRuntimeLogDay(day) {
		return "", errors.New("logarchive: invalid collected spool identity")
	}
	return filepath.Join(s.root, instance, day[:4], day[5:7], spoolFilePrefix+day), nil
}

func collectedMarker(base string) (bool, error) {
	path := base + spoolCollectedSuffix
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len(spoolCollectedMagic)) {
		return false, errors.New("logarchive: invalid local collection marker")
	}
	//nolint:forbidigo // validated small, regular marker under the private spool.
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if string(content) != spoolCollectedMagic {
		return false, errors.New("logarchive: invalid local collection marker")
	}
	return true, nil
}

func (s *Spool) collectedDayMarked(instance, day string) (bool, error) {
	base, err := s.collectionBase(instance, day)
	if err != nil {
		return false, err
	}
	return collectedMarker(base)
}

// The configured root may be a mounted/symlinked operator destination. Every
// component below it must be a real directory; cleanup never follows a peer's
// symlink to data outside that root.
func (s *Spool) collectionDirectory(instance, day string) error {
	if err := os.MkdirAll(s.root, 0o750); err != nil {
		return err
	}
	path := s.root
	for _, component := range []string{instance, day[:4], day[5:7]} {
		path = filepath.Join(path, component)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(path, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(path)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("logarchive: collected spool directory is not a real directory")
		}
	}
	return nil
}

// installCollectedMarker atomically installs a complete witness. The caller
// fsyncs the directory before deleting payloads, including on a resumed pass.
func installCollectedMarker(base string) error {
	if marked, err := collectedMarker(base); err != nil || marked {
		return err
	}
	tmp := base + spoolCollectedSuffix + ".pending"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(spoolCollectedMagic); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, base+spoolCollectedSuffix)
}

// discardCollectedDay is only called after an exact durable collection receipt
// or an already-installed private local witness. Spool.mu fences live writers.
func (s *Spool) discardCollectedDay(instance, day string) (localCollectionResult, error) {
	var result localCollectionResult
	base, err := s.collectionBase(instance, day)
	if err != nil {
		return result, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if s.written < 0 {
			s.written = 0
		}
	}()
	if err := s.collectionDirectory(instance, day); err != nil {
		return result, err
	}
	if err := installCollectedMarker(base); err != nil {
		return result, err
	}
	key := spoolKey{instance: instance, day: day}
	if s.collected == nil {
		s.collected = make(map[spoolKey]bool)
	}
	s.collected[key] = true // Also fence a writer if directory sync needs retry.
	if err := syncArchiveDir(base); err != nil {
		return result, err
	}
	if fh, ok := s.files[key]; ok {
		// This data is explicitly expired and collected; do not require disk
		// space to flush an old buffer before reclaiming its capacity.
		buffered := int64(fh.bw.Buffered())
		closeErr := fh.file.Close()
		delete(s.files, key)
		s.written -= buffered
		result.PendingBytes += buffered
		if closeErr != nil {
			return result, closeErr
		}
	}
	for _, suffix := range []string{spoolPartialSuffix, spoolUploadSuffix, spoolCommittedSuffix, archiveGzipSuffix + ".pending", archiveGzipSuffix} {
		path := base + suffix
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, err
		}
		if !info.Mode().IsRegular() {
			return result, fmt.Errorf("logarchive: collected payload is not a regular file: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return result, err
		}
		result.Files++
		if suffix == spoolPartialSuffix || suffix == spoolUploadSuffix {
			s.written -= info.Size()
			result.PendingBytes += info.Size()
		}
	}
	return result, syncArchiveDir(base)
}

// Caller holds s.mu. Active handles need no filesystem probe: collection uses
// the same mutex, closes the handle, and installs the cached fence first.
func (s *Spool) checkCollectedLocked(key spoolKey) error {
	if s.collected[key] {
		return ErrSpoolDayCollected
	}
	if _, active := s.files[key]; active {
		return nil
	}
	marked, err := s.collectedDayMarked(key.instance, key.day)
	if err != nil {
		return err
	}
	if !marked {
		return nil
	}
	if s.collected == nil {
		s.collected = make(map[spoolKey]bool)
	}
	s.collected[key] = true
	return ErrSpoolDayCollected
}
