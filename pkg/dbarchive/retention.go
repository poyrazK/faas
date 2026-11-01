package dbarchive

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
// files stay: they are tiny and recovery follows them. A hold (holds.go)
// moves the period's start back to its point.

// PruneResult counts what one pass removed.
type PruneResult struct {
	Backups int `json:"backups"`
	WAL     int `json:"wal"`
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

// Prune removes what restoring to cutoff or later does not need, from the
// archive this node keeps (and those files' bucket copies).
func (s *TieredStore) Prune(ctx context.Context, databaseID string, cutoff time.Time) (PruneResult, error) {
	s.maint.Lock()
	defer s.maint.Unlock()
	return s.prune(ctx, databaseID, cutoff, false)
}

// PruneBucket prunes a database's archive as the bucket holds it, to
// max(windowSeconds, MinRetention). schedd asks for it, through any node,
// for databases whose own node is lost: nothing else prunes them, since
// each node prunes from its local files.
func (s *TieredStore) PruneBucket(ctx context.Context, databaseID string, windowSeconds int64) (PruneResult, error) {
	if s.Remote == nil {
		return PruneResult{}, errors.New("dbarchive: no bucket to prune")
	}
	if !ValidDatabaseID(databaseID) || windowSeconds < 0 {
		return PruneResult{}, fmt.Errorf("dbarchive: invalid prune of %q", databaseID)
	}
	minimum := s.MinRetention
	if minimum <= 0 {
		minimum = DefaultMinRetention
	}
	retention := max(time.Duration(windowSeconds)*time.Second, minimum)
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	s.maint.Lock()
	defer s.maint.Unlock()
	return s.prune(ctx, databaseID, now().Add(-retention), true)
}

// DefaultMinRetention is the retention floor when none is configured.
const DefaultMinRetention = 24 * time.Hour

// prune removes backups older than the anchor and WAL before its start
// segment. bucket widens the catalog and the files to the bucket's.
func (s *TieredStore) prune(ctx context.Context, databaseID string, cutoff time.Time, bucket bool) (PruneResult, error) {
	var res PruneResult
	// A hold keeps its point restorable past the retention period.
	floor, held, err := s.holdFloor(ctx, databaseID)
	if err != nil {
		return res, fmt.Errorf("dbarchive: read holds: %w", err)
	}
	if held && floor.Before(cutoff) {
		cutoff = floor
	}
	var backups []BackupManifest
	if bucket {
		backups, err = s.ListAllBackups(ctx, databaseID)
	} else {
		backups, err = s.ListBackups(databaseID)
	}
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
	keep := backups[anchor].StartWAL
	if len(keep) != 24 {
		return res, errors.New("dbarchive: anchor has no start segment")
	}
	baseDir, err := s.baseDir(databaseID)
	if err != nil {
		return res, err
	}
	walDir := filepath.Join(s.dir, databaseID, "wal")
	baseNames, walNames, err := s.archiveNames(ctx, databaseID, baseDir, walDir, bucket)
	if err != nil {
		return res, err
	}
	// By file, not by listed backup, so a pass interrupted between a
	// manifest and its data still removes the rest next time. Names are
	// sorted, so a manifest goes before its data, here and in the bucket.
	for _, name := range baseNames {
		id, ext, ok := strings.Cut(name, ".")
		if !ok || !ValidBackupID(id) || id >= backups[anchor].ID || (ext != "json" && ext != "tar.zst") {
			continue
		}
		if err := s.remove(ctx, databaseID, filepath.Join(baseDir, name), BaseKey(s.Prefix, databaseID, name)); err != nil {
			return res, err
		}
		if ext == "json" {
			res.Backups++
		}
	}
	for _, name := range walNames {
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

// archiveNames lists the database's base-backup and WAL file names, from
// the local directories and, with bucket, the bucket too; sorted, unique.
func (s *TieredStore) archiveNames(ctx context.Context, databaseID, baseDir, walDir string, bucket bool) ([]string, []string, error) {
	base, wal := map[string]bool{}, map[string]bool{}
	for dir, set := range map[string]map[string]bool{baseDir: base, walDir: wal} {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		for _, e := range entries {
			set[e.Name()] = true
		}
	}
	if bucket {
		prefix := strings.TrimSuffix(s.Prefix, "/") + "/" + databaseID + "/"
		keys, err := s.Remote.List(ctx, prefix)
		if err != nil {
			return nil, nil, err
		}
		for _, k := range keys {
			if name, ok := strings.CutPrefix(k, prefix+"base/"); ok {
				base[name] = true
			} else if name, ok := strings.CutPrefix(k, prefix+"wal/"); ok {
				wal[name] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(base)), slices.Sorted(maps.Keys(wal)), nil
}

// remove deletes one archive file: the bucket copy first, so a failure
// leaves the local file to retry from, then the file and its markers. The
// off-site journal records it before anything goes.
func (s *TieredStore) remove(ctx context.Context, databaseID, path, key string) error {
	rel := filepath.Base(filepath.Dir(path)) + "/" + filepath.Base(path)
	if err := s.journalOffsite(offsiteOp{Op: offsiteDelete, Database: databaseID, Path: rel}); err != nil {
		return err
	}
	if s.Remote != nil {
		if err := s.Remote.Delete(ctx, key); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, marker := range []string{uploadedMarker(s.dir, databaseID, filepath.Base(path)), offsiteMarker(s.dir, databaseID, filepath.Base(path))} {
		if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
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
		res, err := s.prune(ctx, e.Name(), now.Add(-retention), false)
		total.add(res)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("dbarchive: prune %s: %w", e.Name(), err)
		}
	}
	return total, firstErr
}

// Drop deletes a deleted database's whole archive: every object under its
// bucket prefix, then the local directory. The off-site journal records it
// first.
func (s *TieredStore) Drop(ctx context.Context, databaseID string) error {
	if !ValidDatabaseID(databaseID) {
		return fmt.Errorf("dbarchive: invalid database id %q", databaseID)
	}
	s.maint.Lock()
	defer s.maint.Unlock()
	if err := s.journalOffsite(offsiteOp{Op: offsiteDrop, Database: databaseID}); err != nil {
		return err
	}
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

// LocalBytes is the size of the database's archive on this node: its WAL
// and base backups. A database with no local archive has zero.
func (s *LocalStore) LocalBytes(databaseID string) (int64, error) {
	sizes, err := s.localSizes(databaseID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, size := range sizes {
		total += size
	}
	return total, nil
}

// ArchiveBytes is the database's whole archive; without a remote, the
// one on this node.
func (s *LocalStore) ArchiveBytes(_ context.Context, databaseID string) (int64, error) {
	return s.LocalBytes(databaseID)
}

// localSizes sizes the database's archive files on this node by their
// path under the database ("wal/<name>", "base/<name>"), as the bucket
// names them too.
func (s *LocalStore) localSizes(databaseID string) (map[string]int64, error) {
	if !ValidDatabaseID(databaseID) {
		return nil, fmt.Errorf("dbarchive: invalid database id %q", databaseID)
	}
	sizes := map[string]int64{}
	for _, sub := range []string{"wal", "base"} {
		entries, err := os.ReadDir(filepath.Join(s.dir, databaseID, sub))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue // an upload or backup in flight
			}
			info, err := e.Info()
			if err != nil {
				continue // pruned meanwhile
			}
			sizes[sub+"/"+e.Name()] = info.Size()
		}
	}
	return sizes, nil
}

// ArchiveBytes is the size of the database's whole archive: every WAL
// file and base backup, in the bucket or still only on this node, each
// counted once. The restore-history meter samples it. After a move or a
// failover the database's earlier history is only in the bucket, so the
// local archive alone would undercount it until the window rolled over.
func (s *TieredStore) ArchiveBytes(ctx context.Context, databaseID string) (int64, error) {
	local, err := s.localSizes(databaseID)
	if err != nil {
		return 0, err
	}
	var total int64
	if s.Remote != nil {
		root := strings.TrimSuffix(s.Prefix, "/") + "/" + databaseID + "/"
		remote, err := s.Remote.Sizes(ctx, root)
		if err != nil {
			return 0, err
		}
		for key, size := range remote {
			rel := strings.TrimPrefix(key, root)
			if !strings.HasPrefix(rel, "wal/") && !strings.HasPrefix(rel, "base/") {
				continue // holds
			}
			total += size
			delete(local, rel)
		}
	}
	for _, size := range local {
		total += size // not uploaded yet
	}
	return total, nil
}
