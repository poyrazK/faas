package buildexport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// ReferenceState is the durable handoff state resolved from PostgreSQL.
type ReferenceState uint8

const (
	// ReferenceUnknown is a legacy/orphaned directory with no durable owner.
	ReferenceUnknown ReferenceState = iota
	// ReferenceActive means imaged may still need the artifact for publication
	// or retry. Byte-pressure cleanup never removes an active handoff.
	ReferenceActive
	// ReferenceReleased means the build/deployment is terminal or the
	// deployment now references the published application layer.
	ReferenceReleased
)

// Resolver maps a build id and its canonical artifact path to durable state.
type Resolver func(context.Context, string, string) (ReferenceState, error)

type SweepOptions struct {
	Root         string
	MaxAge       time.Duration
	OrphanMinAge time.Duration
	MaxBytes     int64
	Now          time.Time
	Resolve      Resolver
}

// SweepResult is one complete inventory and cleanup pass. RemovedByReason has
// the closed keys released, expired, orphaned and pressure.
type SweepResult struct {
	CurrentBytes    int64
	ReclaimedBytes  int64
	Removed         int
	SkippedActive   int
	Errors          int
	RemovedByReason map[string]int
}

type entry struct {
	buildID  string
	path     string
	artifact string
	bytes    int64
	mtime    time.Time
	state    ReferenceState
	removed  bool
}

// Sweep inventories one export root, removes released handoffs immediately,
// expires any artifact beyond MaxAge, and reclaims legacy orphans older than
// OrphanMinAge when MaxBytes is exceeded. A shared reader lease always wins.
func Sweep(ctx context.Context, opts SweepOptions) (SweepResult, error) {
	result := SweepResult{RemovedByReason: map[string]int{}}
	if opts.Root == "" {
		return result, errors.New("build export: empty root")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	dirs, err := os.ReadDir(opts.Root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("build export: read root: %w", err)
	}
	entries := make([]*entry, 0, len(dirs))
	for _, dir := range dirs {
		if !dir.IsDir() || dir.Name() == "" || dir.Name()[0] == '.' {
			continue
		}
		item := &entry{
			buildID:  dir.Name(),
			path:     filepath.Join(opts.Root, dir.Name()),
			artifact: filepath.Join(opts.Root, dir.Name(), "build", "out", "image.tar"),
		}
		item.bytes, item.mtime, err = treeUsage(item.path)
		if err != nil {
			result.Errors++
			continue
		}
		result.CurrentBytes += item.bytes
		if opts.Resolve != nil {
			item.state, err = opts.Resolve(ctx, item.buildID, item.artifact)
			if err != nil {
				result.Errors++
				continue
			}
		}
		entries = append(entries, item)
	}

	for _, item := range entries {
		age := opts.Now.Sub(item.mtime)
		reason := ""
		switch {
		case item.state == ReferenceReleased:
			reason = "released"
		case opts.MaxAge > 0 && age >= opts.MaxAge:
			reason = "expired"
		}
		if reason != "" {
			removeEntry(item, reason, &result)
		}
	}

	if opts.MaxBytes > 0 && result.CurrentBytes > opts.MaxBytes {
		sort.Slice(entries, func(i, j int) bool { return entries[i].mtime.Before(entries[j].mtime) })
		for _, item := range entries {
			if result.CurrentBytes <= opts.MaxBytes {
				break
			}
			// A rootfs_path reference is a durable imaged lease between retry
			// attempts. Pressure may reclaim only unowned legacy output after
			// the conservative age floor.
			if item.removed || item.state == ReferenceActive ||
				(opts.OrphanMinAge > 0 && opts.Now.Sub(item.mtime) < opts.OrphanMinAge) {
				continue
			}
			removeEntry(item, "pressure", &result)
		}
	}
	for _, item := range entries {
		if !item.removed && item.state == ReferenceActive {
			result.SkippedActive++
		}
	}
	return result, nil
}

func removeEntry(item *entry, reason string, result *SweepResult) {
	if item.removed {
		return
	}
	f, err := os.Open(item.artifact)
	if err == nil {
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
				result.SkippedActive++
				return
			}
			result.Errors++
			return
		}
		defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck -- best effort after delete
	} else if !errors.Is(err, os.ErrNotExist) {
		result.Errors++
		return
	}
	if err := os.RemoveAll(item.path); err != nil {
		result.Errors++
		return
	}
	item.removed = true
	result.Removed++
	result.RemovedByReason[reason]++
	result.ReclaimedBytes += item.bytes
	result.CurrentBytes -= item.bytes
}

func treeUsage(root string) (int64, time.Time, error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, time.Time{}, err
	}
	mtime := info.ModTime()
	var bytes int64
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			bytes += fi.Size()
			if fi.ModTime().After(mtime) {
				mtime = fi.ModTime()
			}
		}
		return nil
	})
	return bytes, mtime, err
}
