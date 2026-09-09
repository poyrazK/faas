// Provider-neutral PostgreSQL backup and local WAL-retention sampler.
//
// Root-owned backup jobs publish only non-sensitive timestamps and aggregate
// sizes under /var/lib/faas/backup-state. apid reads those files once a minute;
// it never needs access to PostgreSQL's data, basebackup, or WAL directories.

package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

const pgBackupPushedInterval = 60 * time.Second

var pgBackupStateRoot = "/var/lib/faas/backup-state"

type pgBackupPushedSampler struct {
	ops *wire.OpsMetrics
	log *slog.Logger
}

func newPgBackupPushedSampler(ops *wire.OpsMetrics, log *slog.Logger) *pgBackupPushedSampler {
	return &pgBackupPushedSampler{ops: ops, log: log}
}

func (s *pgBackupPushedSampler) run(ctx context.Context) {
	if s.ops == nil {
		s.log.Warn("pgBackupPushedSampler started with nil ops; exiting")
		return
	}
	ticker := time.NewTicker(pgBackupPushedInterval)
	defer ticker.Stop()
	s.tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *pgBackupPushedSampler) tick() {
	if s.ops == nil {
		return
	}
	now := time.Now()
	backupStamp, backupOK := readRFC3339Marker(pgBackupStateRoot + "/basebackup-push-success")
	setTimestampGauge(s.ops.PgBackupLastPushed(), backupStamp, backupOK)
	pruneStamp, pruneOK := readRFC3339Marker(pgBackupStateRoot + "/wal-prune-success")
	setTimestampGauge(s.ops.PgWalPruneLastSuccessful(), pruneStamp, pruneOK)

	bytes, oldest, newest, ok := readWalArchiveStats(pgBackupStateRoot + "/wal-archive-stats")
	if !ok {
		s.ops.PgWalArchiveBytes().Set(0)
		s.ops.PgWalArchiveOldestAge().Set(0)
		s.ops.PgWalArchiveNewestAge().Set(0)
		return
	}
	s.ops.PgWalArchiveBytes().Set(float64(bytes))
	s.ops.PgWalArchiveOldestAge().Set(timestampAgeSeconds(now, oldest))
	s.ops.PgWalArchiveNewestAge().Set(timestampAgeSeconds(now, newest))
}

func setTimestampGauge(gauge interface{ Set(float64) }, stamp time.Time, ok bool) {
	if gauge == nil {
		return
	}
	if !ok {
		gauge.Set(0)
		return
	}
	gauge.Set(float64(stamp.Unix()))
}

func readRFC3339Marker(path string) (time.Time, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	return stamp, err == nil
}

func readWalArchiveStats(path string) (bytes int64, oldest, newest time.Time, ok bool) {
	rawFile, err := os.ReadFile(path)
	if err != nil {
		return 0, time.Time{}, time.Time{}, false
	}
	values := make(map[string]int64, 3)
	for _, line := range strings.Split(strings.TrimSpace(string(rawFile)), "\n") {
		key, raw, found := strings.Cut(line, "=")
		if !found {
			return 0, time.Time{}, time.Time{}, false
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return 0, time.Time{}, time.Time{}, false
		}
		values[key] = value
	}
	bytes, hasBytes := values["bytes"]
	oldestUnix, hasOldest := values["oldest_timestamp_seconds"]
	newestUnix, hasNewest := values["newest_timestamp_seconds"]
	if !hasBytes || !hasOldest || !hasNewest {
		return 0, time.Time{}, time.Time{}, false
	}
	return bytes, time.Unix(oldestUnix, 0), time.Unix(newestUnix, 0), true
}

func timestampAgeSeconds(now, stamp time.Time) float64 {
	if stamp.Unix() <= 0 {
		return 0
	}
	age := now.Sub(stamp).Seconds()
	if age < 0 {
		return 0
	}
	return age
}
