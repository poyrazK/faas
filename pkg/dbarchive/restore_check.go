package dbarchive

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	RestoreCovered      = "covered"
	RestoreNoBaseBackup = "no_base_backup"
	RestoreWALGap       = "wal_gap"
	RestoreUnverified   = "unverified"
)

// RestoreCheck reports archive evidence at a requested target.
type RestoreCheck struct {
	Outcome         string     `json:"outcome"`
	Reason          string     `json:"reason"`
	BackupCompleted *time.Time `json:"backup_completed_at,omitempty"`
	WALStopTime     *time.Time `json:"wal_stop_at,omitempty"`
	MissingWAL      string     `json:"missing_wal_segment,omitempty"`
	MissingHistory  string     `json:"missing_timeline_history,omitempty"`
	Timeline        uint32     `json:"recovery_timeline,omitempty"`
	RecoveryPoint   *time.Time `json:"recovery_point,omitempty"`
}

type restoreCheckCatalog interface {
	checkBackups(context.Context, string) ([]BackupManifest, error)
	checkWALNames(context.Context, string) (map[string]bool, error)
}

var backupStopLSN = regexp.MustCompile(`(?m)^STOP WAL LOCATION: ([0-9A-F]+/[0-9A-F]+)(?:[ \t]|$)`)

func (s *Server) checkRestore(ctx context.Context, id string, target time.Time, major int) RestoreCheck {
	return s.inspectRestore(ctx, id, target, major, false, 0)
}

func (s *Server) inspectRestore(ctx context.Context, id string, target time.Time, major int, latest bool, pinned uint32) RestoreCheck {
	check := RestoreCheck{Outcome: RestoreUnverified, Reason: "archive_unavailable"}
	catalog, ok := s.Backups.(restoreCheckCatalog)
	if !ok || s.Store == nil {
		return check
	}

	backup, err := s.Backups.SelectBackup(ctx, s.Store, id, target)
	if errors.Is(err, ErrNotFound) {
		if noSuitableBackup(ctx, s.Store, catalog, id, target) {
			return RestoreCheck{Outcome: RestoreNoBaseBackup, Reason: "no_suitable_backup"}
		}
		check.Reason = "backup_history_unverified"
		return check
	}
	if err != nil {
		return check
	}
	history, err := readBackupHistory(ctx, s.Store, id, backup)
	if err != nil {
		check.Reason = "backup_history_unverified"
		return check
	}
	completed, err := backupHistoryStopTime(history)
	if err != nil || completed.After(target) {
		check.Reason = "backup_history_unverified"
		return check
	}
	completed = completed.UTC()
	check.BackupCompleted = &completed
	start, startOK := parseRestoreLSN(backup.StartLSN)
	stopMatch := backupStopLSN.FindSubmatch(history)
	if !startOK || len(stopMatch) != 2 {
		check.Reason = "backup_history_unverified"
		return check
	}
	end, endOK := parseRestoreLSN(string(stopMatch[1]))
	if !endOK || end <= start {
		check.Reason = "backup_history_unverified"
		return check
	}
	if !selectedBackupReadable(ctx, s.Backups, id, backup) {
		check.Reason = "backup_unreadable"
		return check
	}
	names, err := catalog.checkWALNames(ctx, id)
	if err != nil {
		return check
	}
	lineage, timeline, missingHistory, reason := resolveRestoreLineage(ctx, s.Store, id, names, backup.Timeline, start, end, pinned)
	check.MissingHistory = missingHistory
	if reason != "" || backup.StartWAL != timelineWALName(uint32(backup.Timeline), start/walSegmentBytes) {
		check.Reason = "timeline_unverified"
		if reason != "" {
			check.Reason = reason
		}
		return check
	}
	if walPageMagic(major) == 0 {
		check.Reason = "wal_format_unverified"
		return check
	}

	check.Timeline = timeline
	reader := restoreWALReader{store: s.Store, id: id, magic: walPageMagic(major), names: names, backupEnd: end, lineage: lineage}
	timestamp, missing, reason := reader.scan(ctx, start, target, latest)
	check.Reason = reason
	if latest && !timestamp.IsZero() {
		point := timestamp.Add(-time.Microsecond)
		if point.After(target) {
			point = target
		}
		if !point.Before(completed) {
			check.Outcome, check.WALStopTime, check.RecoveryPoint, check.MissingWAL = RestoreCovered, &timestamp, &point, missing
			if check.Reason == "target_not_reached" {
				check.Reason = "archive_covered"
			}
			return check
		}
		timestamp = time.Time{}
	}
	switch {
	case missing != "":
		check.Outcome, check.MissingWAL = RestoreWALGap, missing
	case !timestamp.IsZero():
		check.Outcome, check.WALStopTime = RestoreCovered, &timestamp
	}
	return check
}

func noSuitableBackup(ctx context.Context, wal Store, catalog restoreCheckCatalog, id string, target time.Time) bool {
	backups, err := catalog.checkBackups(ctx, id)
	if err != nil {
		return false
	}
	for _, backup := range backups {
		if backup.StartTime.After(target) {
			continue
		}
		stop, err := backupStopTime(ctx, wal, id, backup)
		if err != nil || !stop.After(target) {
			return false
		}
	}
	return true
}

func selectedBackupReadable(ctx context.Context, store BackupStore, id string, backup BackupManifest) bool {
	rc, err := store.OpenBackup(ctx, id, backup.ID)
	if err != nil {
		return false
	}
	defer func() { _ = rc.Close() }()
	// Native pg_basebackup tar files place the label ahead of the data.
	label, err := readBackupLabel(io.LimitReader(rc, 1<<20))
	return err == nil && label.StartLSN == backup.StartLSN && label.StartWAL == backup.StartWAL &&
		label.Timeline == backup.Timeline && label.StartTime.Equal(backup.StartTime)
}

func (s *LocalStore) checkBackups(_ context.Context, id string) ([]BackupManifest, error) {
	return s.ListBackups(id)
}

func (s *TieredStore) checkBackups(ctx context.Context, id string) ([]BackupManifest, error) {
	backups, err := s.LocalStore.checkBackups(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, tier := range s.checkTiers() {
		// Read each tier's manifests before deduplicating with other tiers.
		// A missing history for an older candidate leaves the answer unknown.
		items, err := listRemoteBackups(ctx, tier.remote, BaseKey(tier.prefix, id, ""), nil)
		if err != nil {
			return nil, err
		}
		backups = append(backups, items...)
	}
	return backups, nil
}

func (s *LocalStore) checkWALNames(_ context.Context, id string) (map[string]bool, error) {
	path, err := s.walPath(id, "000000010000000000000000")
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && ValidWALName(entry.Name()) {
			names[entry.Name()] = true
		}
	}
	return names, nil
}

func (s *TieredStore) checkWALNames(ctx context.Context, id string) (map[string]bool, error) {
	names, err := s.LocalStore.checkWALNames(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, tier := range s.checkTiers() {
		prefix := WALKey(tier.prefix, id, "")
		keys, err := tier.remote.List(ctx, prefix)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			if name, ok := strings.CutPrefix(key, prefix); ok && ValidWALName(name) {
				names[name] = true
			}
		}
	}
	return names, nil
}

type restoreCheckTier struct {
	remote Remote
	prefix string
}

func (s *TieredStore) checkTiers() []restoreCheckTier {
	tiers := []restoreCheckTier{{s.Remote, s.Prefix}, {s.Offsite, s.OffsitePrefix}}
	return slices.DeleteFunc(tiers, func(tier restoreCheckTier) bool { return tier.remote == nil })
}

func parseRestoreLSN(raw string) (uint64, bool) {
	high, low, ok := strings.Cut(raw, "/")
	if !ok {
		return 0, false
	}
	h, errHigh := strconv.ParseUint(high, 16, 32)
	l, errLow := strconv.ParseUint(low, 16, 32)
	return h<<32 | l, errHigh == nil && errLow == nil && (h != 0 || l != 0)
}

func (s *Server) handleRestoreCheck(ctx context.Context, conn io.Writer, id string, req Request) {
	target, err := time.Parse(time.RFC3339Nano, req.Target)
	if err != nil || req.Major < 12 || req.Major > 99 || (req.Timeline != 0 && (req.Op != OpCheckRecovery || req.Latest)) {
		_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
		return
	}
	defer s.restoreChecks.Add(-1)
	if s.restoreChecks.Add(1) > 2 {
		_ = WriteLine(conn, Response{RestoreCheck: &RestoreCheck{Outcome: RestoreUnverified, Reason: "archive_busy"}})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var result RestoreCheck
	if req.Op == OpCheckRecovery {
		if s.Recovery == nil {
			result = RestoreCheck{Outcome: RestoreUnverified, Reason: "recovery_archive_unconfigured"}
		} else {
			recovery := Server{Store: s.Recovery, Backups: s.Recovery}
			result = recovery.inspectRestore(ctx, id, target, req.Major, req.Latest, req.Timeline)
		}
	} else {
		result = s.inspectRestore(ctx, id, target, req.Major, req.Latest, req.Timeline)
	}
	_ = WriteLine(conn, Response{RestoreCheck: &result})
}

// CheckArchiveRestore inspects archived evidence without contacting a guest.
func CheckArchiveRestore(ctx context.Context, socket, id string, target time.Time, major int) (RestoreCheck, error) {
	return checkArchive(ctx, socket, id, Request{Op: OpCheckRestore, Target: target.UTC().Format(time.RFC3339Nano), Major: major})
}

// CheckLatestArchiveRestore finds a recoverable point in the primary archive.
func CheckLatestArchiveRestore(ctx context.Context, socket, id string, target time.Time, major int) (RestoreCheck, error) {
	return checkArchive(ctx, socket, id, Request{Op: OpCheckRestore, Target: target.UTC().Format(time.RFC3339Nano), Major: major, Latest: true})
}

func CheckRecoveryArchive(ctx context.Context, socket, id string, target time.Time, major int, latest bool, timeline uint32) (RestoreCheck, error) {
	return checkArchive(ctx, socket, id, Request{Op: OpCheckRecovery, Target: target.UTC().Format(time.RFC3339Nano), Major: major, Latest: latest, Timeline: timeline})
}

func checkArchive(ctx context.Context, socket, id string, req Request) (RestoreCheck, error) {
	resp, err := admin(ctx, socket, id, req)
	if err != nil {
		return RestoreCheck{}, err
	}
	if resp.RestoreCheck == nil {
		return RestoreCheck{}, errors.New("dbarchive: restore check response is missing evidence")
	}
	return *resp.RestoreCheck, nil
}
