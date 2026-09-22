package logarchive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const archiveGzipSuffix = ".jsonl.gz"

// commitArchive installs the cumulative gzip only after S3 accepted it. The
// sealed fragment becomes a commit marker before the new history is installed:
// a restart can then finish the rename without appending that fragment twice.
// Before the marker exists, retrying uploads the same complete replacement.
func (s *Spool) commitArchive(upload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	base := strings.TrimSuffix(upload, spoolUploadSuffix)
	info, err := os.Stat(upload)
	if err != nil {
		return fmt.Errorf("logarchive: stat accepted upload: %w", err)
	}
	if err := os.Rename(upload, base+spoolCommittedSuffix); err != nil {
		return fmt.Errorf("logarchive: mark accepted upload: %w", err)
	}
	// The committed fragment is no longer pending, even if installing the
	// history below needs another pass. NewSpool uses the same accounting.
	s.written -= info.Size()
	if s.written < 0 {
		s.written = 0
	}
	if err := syncArchiveDir(base); err != nil {
		return err
	}
	return finishArchiveCommit(base)
}

// finishArchiveCommit runs under Spool.mu before any new daily fragment is
// sealed. Both crash points are recoverable: pending exists before promotion;
// only history exists after promotion. Never discard a marker with neither.
func finishArchiveCommit(base string) error {
	marker := base + spoolCommittedSuffix
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("logarchive: stat commit marker: %w", err)
	}
	history := base + archiveGzipSuffix
	pending := history + ".pending"
	if _, err := os.Stat(pending); err == nil {
		if err := os.Rename(pending, history); err != nil {
			return fmt.Errorf("logarchive: install committed history: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("logarchive: stat pending history: %w", err)
	} else if _, err := os.Stat(history); err != nil {
		return fmt.Errorf("logarchive: missing committed history: %w", err)
	}
	if err := syncArchiveDir(base); err != nil {
		return err
	}
	if err := os.Remove(marker); err != nil {
		return fmt.Errorf("logarchive: remove commit marker: %w", err)
	}
	return syncArchiveDir(base)
}

func syncArchiveDir(base string) error {
	//nolint:forbidigo // base is a validated spool-owned daily archive path.
	dir, err := os.Open(filepath.Dir(base))
	if err != nil {
		return fmt.Errorf("logarchive: open archive directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("logarchive: sync archive directory: %w", err)
	}
	return nil
}
