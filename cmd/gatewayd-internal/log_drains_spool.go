package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// appLogDrainSpoolOrphanTTL is how long a drain's spool stays on this node
// after nothing here uses it. A drain that comes back within it (re-enabled,
// or its app's ownership returning) resumes from its cursor and backlog.
// After it the spool goes, with its dead letters: raw customer log lines of
// deleted drains, apps and accounts must not stay on the node.
const appLogDrainSpoolOrphanTTL = 24 * time.Hour

// appLogDrainSpoolBudgetBytes parses FAAS_LOG_DRAIN_SPOOL_BUDGET_BYTES; an
// empty or invalid value selects the default node budget.
func appLogDrainSpoolBudgetBytes(raw string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// pruneSpoolsLocked removes the spool directories of drains that are not in
// keep and have not been written for appLogDrainSpoolOrphanTTL. Only the
// manager holding the spool lease calls it.
func (m *appLogDrainManager) pruneSpoolsLocked(ctx context.Context, keep map[string]struct{}, now time.Time) {
	entries, err := os.ReadDir(m.spoolRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			m.log.WarnContext(ctx, "list log drain spools", slog.String("err", err.Error()))
		}
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if _, ok := keep[name]; ok {
			continue
		}
		dir := filepath.Join(m.spoolRoot, name)
		last, ok := appLogDrainSpoolLastWrite(dir)
		if !ok || now.Sub(last) < appLogDrainSpoolOrphanTTL {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			m.log.WarnContext(ctx, "remove orphaned log drain spool", slog.String("drain_id", name), slog.String("err", err.Error()))
			continue
		}
		m.log.InfoContext(ctx, "removed orphaned log drain spool", slog.String("drain_id", name), slog.Time("last_write", last))
	}
}

// appLogDrainSpoolLastWrite is the newest modification time of a spool
// directory or any file in it.
func appLogDrainSpoolLastWrite(dir string) (time.Time, bool) {
	info, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, false
	}
	last := info.ModTime()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	for _, entry := range entries {
		fi, err := entry.Info()
		if err != nil {
			return time.Time{}, false
		}
		if fi.ModTime().After(last) {
			last = fi.ModTime()
		}
	}
	return last, true
}
