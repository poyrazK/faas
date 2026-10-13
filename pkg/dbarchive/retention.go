package dbarchive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Retention (ADR-1027 durability). A database's archive must restore to
// any instant in its retention period, max(restore window, the node's
// minimum). The anchor is the newest base backup consistent at or before
// the period's start: it and every newer backup stay, and so does all WAL
// from the anchor's start segment on. Older backups, WAL segments and
// backup history files go, locally and in the bucket. Timeline history
// files stay: they are tiny and recovery follows them.

// PruneResult counts what one pass removed.
type PruneResult struct {
	Backups int
	WAL     int
}

func (r *PruneResult) add(o PruneResult) {
	r.Backups += o.Backups
	r.WAL += o.WAL
}

const windowFile = "restore-window"

// NoteWindow records the database's restore window, as the relay header
// last stated it. It rewrites the file only when the value changes.
func (s *LocalStore) NoteWindow(databaseID string, seconds int64) error {
	if !ValidDatabaseID(databaseID) || seconds < 0 {
		return fmt.Errorf("dbarchive: invalid window for %q", databaseID)
	}
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	if s.windows == nil {
		s.windows = map[string]int64{}
	}
	if known, ok := s.windows[databaseID]; ok && known == seconds {
		return nil
	}
	dir := filepath.Join(s.dir, databaseID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(dir, windowFile), []byte(strconv.FormatInt(seconds, 10)+"\n")); err != nil {
		return err
	}
	s.windows[databaseID] = seconds
	return nil
}

// Window returns the recorded restore window; zero when none was stated.
func (s *LocalStore) Window(databaseID string) int64 {
	raw, err := os.ReadFile(filepath.Join(s.dir, databaseID, windowFile)) //nolint:gosec // under the store root
	if err != nil {
		return 0
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return seconds
}

// Prune removes what restoring to cutoff or later does not need.
func (s *TieredStore) Prune(ctx context.Context, databaseID string, cutoff time.Time) (PruneResult, error) {
	s.maint.Lock()
	defer s.maint.Unlock()
	return s.prune(ctx, databaseID, cutoff)
}

func (s *TieredStore) prune(ctx context.Context, databaseID string, cutoff time.Time) (PruneResult, error) {
	var res PruneResult
	// The local catalog: pruning acts on the archive this node keeps.
	backups, err := s.ListBackups(databaseID)
	if err != nil {
		return res, err
	}
	anchor := -1
	for i := len(backups) - 1; i >= 0; i-- {
		// A backup whose history file is not archived yet has no known
		// stop time: it cannot anchor, and newer backups are kept anyway.
		stop, err := backupStopTime(ctx, s, databaseID, backups[i])
		if err == nil && !stop.After(cutoff) {
			anchor = i
			break
		}
	}
	if anchor < 0 {
		return res, nil
	}
	baseDir, err := s.baseDir(databaseID)
	if err != nil {
		return res, err
	}
	// By file, not by listed backup, so a pass interrupted between a
	// manifest and its data still removes the rest next time. ReadDir
	// sorts, so a manifest goes before its data, here and in the bucket.
	files, err := os.ReadDir(baseDir)
	if err != nil {
		return res, err
	}
	for _, f := range files {
		id, ext, ok := strings.Cut(f.Name(), ".")
		if !ok || !ValidBackupID(id) || id >= backups[anchor].ID || (ext != "json" && ext != "tar.zst") {
			continue
		}
		if err := s.remove(ctx, databaseID, filepath.Join(baseDir, f.Name()), BaseKey(s.Prefix, databaseID, f.Name())); err != nil {
			return res, err
		}
		if ext == "json" {
			res.Backups++
		}
	}
	keep := backups[anchor].StartWAL
	if len(keep) != 24 {
		return res, errors.New("dbarchive: anchor has no start segment")
	}
	walDir := filepath.Join(s.dir, databaseID, "wal")
	entries, err := os.ReadDir(walDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return res, err
	}
	for _, e := range entries {
		name := e.Name()
		if !ValidWALName(name) || strings.HasSuffix(name, ".history") {
			continue
		}
		// Segment numbers, not timelines, order WAL: recovery from the
		// anchor never reads an earlier segment on any timeline.
		if name[8:24] >= keep[8:24] {
			continue
		}
		if err := s.remove(ctx, databaseID, filepath.Join(walDir, name), WALKey(s.Prefix, databaseID, name)); err != nil {
			return res, err
		}
		res.WAL++
	}
	return res, nil
}

// remove deletes one archive file: the bucket copy first, so a failure
// leaves the local file to retry from, then the file and its marker.
func (s *TieredStore) remove(ctx context.Context, databaseID, path, key string) error {
	if s.Remote != nil {
		if err := s.Remote.Delete(ctx, key); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(uploadedMarker(s.dir, databaseID, filepath.Base(path))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// PruneAll prunes every database on the node, each to now minus
// max(its restore window, minRetention). It goes on past a failing
// database and returns the first error.
func (s *TieredStore) PruneAll(ctx context.Context, now time.Time, minRetention time.Duration) (PruneResult, error) {
	s.maint.Lock()
	defer s.maint.Unlock()
	var total PruneResult
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return total, err
	}
	var firstErr error
	for _, e := range entries {
		if !e.IsDir() || !ValidDatabaseID(e.Name()) {
			continue
		}
		retention := max(time.Duration(s.Window(e.Name()))*time.Second, minRetention)
		res, err := s.prune(ctx, e.Name(), now.Add(-retention))
		total.add(res)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("dbarchive: prune %s: %w", e.Name(), err)
		}
	}
	return total, firstErr
}

// Drop deletes a deleted database's whole archive: every object under its
// bucket prefix, then the local directory.
func (s *TieredStore) Drop(ctx context.Context, databaseID string) error {
	if !ValidDatabaseID(databaseID) {
		return fmt.Errorf("dbarchive: invalid database id %q", databaseID)
	}
	s.maint.Lock()
	defer s.maint.Unlock()
	if s.Remote != nil {
		keys, err := s.Remote.List(ctx, strings.TrimSuffix(s.Prefix, "/")+"/"+databaseID+"/")
		if err != nil {
			return err
		}
		for _, key := range keys {
			if err := s.Remote.Delete(ctx, key); err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(filepath.Join(s.dir, databaseID)); err != nil {
		return err
	}
	s.windowsMu.Lock()
	delete(s.windows, databaseID)
	s.windowsMu.Unlock()
	return nil
}
