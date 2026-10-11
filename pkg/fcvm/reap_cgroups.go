package fcvm

// Orphaned tenant cgroup reclamation — ADR-631 (H4-61).
//
// Destroy removes an instance's cgroup scope, but a VM that is still running
// when vmmd restarts is torn down by the next daemon's recovery path, which
// leaves the scope directory behind. On production-us every rollout left one
// empty scope per running VM (19 across two nodes in a week). They hold no
// processes or memory, but they accumulate without bound and fail leakcheck.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TenantCgroupReapOptions configures the sweep of per-instance tenant cgroup
// scopes. IsLive must report every instance something still owns (this
// Manager, the resource journal, or durable state) as live.
type TenantCgroupReapOptions struct {
	// Root is the cgroup v2 mount; empty means the host's.
	Root string
	// Parents are the plan slices, relative to Root; empty means every
	// plan's current and legacy slice.
	Parents []string
	IsLive  LiveInstanceFunc
	Log     *slog.Logger
	MinAge  time.Duration
	now     func() time.Time
}

// TenantCgroupReapReport records one sweep's outcome.
type TenantCgroupReapReport struct {
	Scanned        int
	Reaped         int
	SkippedLive    int
	SkippedYoung   int
	SkippedBusy    int
	SkippedUnknown int
	Failed         int
}

// tenantCgroupParents lists every plan's tenant slice, current and legacy.
func tenantCgroupParents() []string {
	seen := map[string]bool{}
	var out []string
	for _, plan := range api.Plans {
		for _, parent := range []string{ParentCgroupFor(plan), LegacyParentCgroupFor(plan)} {
			if parent != "" && !seen[parent] {
				seen[parent] = true
				out = append(out, parent)
			}
		}
	}
	return out
}

// ReapOrphanedTenantCgroups removes aged, instance-named tenant cgroup scopes
// that nothing owns and that hold no process. A populated scope is never
// touched: the sweep only removes directories the kernel already considers
// empty, so it cannot affect a running VM.
func ReapOrphanedTenantCgroups(ctx context.Context, opts TenantCgroupReapOptions) (TenantCgroupReapReport, error) {
	var rep TenantCgroupReapReport
	if opts.IsLive == nil {
		return rep, errors.New("fcvm: reap tenant cgroups: nil IsLive; refusing to sweep without a liveness gate")
	}
	if opts.Root == "" {
		opts.Root = cgroupRoot
	}
	if len(opts.Parents) == 0 {
		opts.Parents = tenantCgroupParents()
	}
	if opts.MinAge <= 0 {
		opts.MinAge = DefaultReapMinAge
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	for _, parent := range opts.Parents {
		entries, err := os.ReadDir(filepath.Join(opts.Root, parent))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return rep, fmt.Errorf("fcvm: reap tenant cgroups: read %q: %w", parent, err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return rep, err
			}
			if !entry.IsDir() || !looksLikeInstanceID(entry.Name()) {
				continue
			}
			rep.Scanned++
			scope := filepath.Join(opts.Root, parent, entry.Name())
			info, err := entry.Info()
			if err != nil {
				rep.SkippedUnknown++
				continue
			}
			if opts.now().Sub(info.ModTime()) < opts.MinAge {
				rep.SkippedYoung++
				continue
			}
			live, err := opts.IsLive(ctx, entry.Name())
			if err != nil {
				rep.SkippedUnknown++
				continue
			}
			if live {
				rep.SkippedLive++
				continue
			}
			populated, err := cgroupPopulated(scope)
			if err != nil {
				rep.SkippedUnknown++
				continue
			}
			if populated {
				rep.SkippedBusy++
				continue
			}
			if err := removeEmptyCgroup(scope); err != nil {
				rep.Failed++
				opts.Log.Warn("fcvm: reap tenant cgroup", "instance", entry.Name(), "err", err)
				continue
			}
			rep.Reaped++
		}
	}
	return rep, nil
}

// cgroupPopulated reads cgroup.events: populated is 1 while the scope or any
// descendant holds a process.
func cgroupPopulated(scope string) (bool, error) {
	f, err := os.Open(filepath.Join(scope, "cgroup.events")) //nolint:forbidigo // kernel cgroupfs interface file under the vetted tenant slice.
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if key, value, ok := strings.Cut(s.Text(), " "); ok && key == "populated" {
			return value != "0", nil
		}
	}
	if err := s.Err(); err != nil {
		return false, err
	}
	return false, errors.New("cgroup.events has no populated key")
}

// removeEmptyCgroup removes scope and its descendant cgroups, deepest first.
// rmdir succeeds only for an empty cgroup, so a process that joins after the
// populated check makes the removal fail rather than strand it.
func removeEmptyCgroup(scope string) error {
	entries, err := os.ReadDir(scope)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if err := removeEmptyCgroup(filepath.Join(scope, entry.Name())); err != nil {
				return err
			}
		}
	}
	return rmdirCgroup(scope)
}

// rmdirCgroup is os.Remove (rmdir on cgroupfs); tests use a plain directory.
var rmdirCgroup = os.Remove
