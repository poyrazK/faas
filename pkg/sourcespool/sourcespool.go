// Package sourcespool removes deployment source archives and build logs from
// a host's build spool once no deployment can need them (ADR-20261026120000).
// Before it, every source deploy left its archive (up to the plan's source
// cap) and its build-log directory on the apid host, the builder host and,
// for pushes, the githubd host for good.
//
// A file is removed only when all of these hold:
//   - its name matches a layout this package owns (Layout), so files other
//     components keep in the same tree are never touched;
//   - it, or for a log directory every file in it, was last written before
//     the retention cutoff;
//   - no deployment that can still build or image references it
//     (state.SourceSpoolStore). A store error removes nothing.
//
// Upload-session .part files belong to apid's upload reaper and are skipped.
package sourcespool

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// Layout names which files under Root belong to the sweeper.
type Layout int

const (
	// BuildSpool is apid's and builderd's FAAS_SPOOL_ROOT: top-level
	// <token>.tar.gz source archives (and their .tmp leftovers), gregale dev
	// sources, builderd download temps, projects/** staged workload
	// archives, and <deployment or build id>/build.log directories.
	BuildSpool Layout = iota
	// GithubdStaging is githubd's build-sources tree:
	// <account>/<app>/<commit>/source.tar.gz and its .source-* temps.
	GithubdStaging
)

var (
	spoolArchive   = regexp.MustCompile(`^[0-9a-f]{24}\.tar\.gz(\.tmp)?$`)
	devArchive     = regexp.MustCompile(`^dev-source-[^/]+\.tar\.gz$`)
	downloadTemp   = regexp.MustCompile(`^\.source-download-[^/]+$`)
	githubdArchive = regexp.MustCompile(`^(source\.tar\.gz|\.source-[^/]+\.tar\.gz)$`)
	projectArchive = regexp.MustCompile(`\.tar\.gz$`)
)

// Sweeper removes expired spool files under Root.
type Sweeper struct {
	Root   string
	Layout Layout
	Store  state.SourceSpoolStore
	MaxAge time.Duration
	Now    func() time.Time
	Log    *slog.Logger
}

// Run sweeps once immediately and then every interval until ctx ends.
func (s Sweeper) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n, err := s.SweepOnce(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("source spool sweep", "root", s.Root, "removed", n, "err", err)
		} else if n > 0 {
			s.log().Info("source spool sweep", "root", s.Root, "removed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweepOnce removes every expired, unreferenced file the layout owns and
// returns how many files it removed.
func (s Sweeper) SweepOnce(ctx context.Context) (int, error) {
	if s.Root == "" || s.Store == nil || s.MaxAge <= 0 {
		return 0, errors.New("sourcespool: root, store and max age are required")
	}
	inFlight, err := s.Store.ListInFlightDeploymentSpoolPaths(ctx)
	if err != nil {
		return 0, fmt.Errorf("sourcespool: list in-flight paths: %w", err)
	}
	keep := make(map[string]bool, len(inFlight))
	for _, p := range inFlight {
		keep[filepath.Clean(p)] = true
		keep[filepath.Dir(filepath.Clean(p))+string(filepath.Separator)] = true
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	sw := sweep{Sweeper: s, keep: keep, cutoff: now.Add(-s.MaxAge)}
	if s.Layout == GithubdStaging {
		err = sw.tree(ctx, s.Root, githubdArchive)
	} else {
		err = sw.buildSpool(ctx)
	}
	return sw.removed, errors.Join(append(sw.errs, err)...)
}

type sweep struct {
	Sweeper
	keep    map[string]bool
	cutoff  time.Time
	removed int
	errs    []error
}

func (sw *sweep) buildSpool(ctx context.Context) error {
	entries, err := os.ReadDir(sw.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("sourcespool: read %s: %w", sw.Root, err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := filepath.Join(sw.Root, e.Name())
		switch {
		case e.Type().IsRegular() && (spoolArchive.MatchString(e.Name()) || devArchive.MatchString(e.Name()) || downloadTemp.MatchString(e.Name())):
			sw.file(p)
		case e.IsDir() && e.Name() == "projects":
			if err := sw.tree(ctx, p, projectArchive); err != nil {
				return err
			}
		case e.IsDir() && isUUID(e.Name()):
			sw.logDir(p)
		}
	}
	return nil
}

// tree removes matching regular files at any depth below dir, then the
// directories the removals left empty. Symlinks are never followed.
func (sw *sweep) tree(ctx context.Context, dir string, match *regexp.Regexp) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir {
				dirs = append(dirs, p)
			}
			return nil
		}
		if d.Type().IsRegular() && match.MatchString(d.Name()) {
			sw.file(p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		sw.emptyDir(dirs[i])
	}
	return err
}

// emptyDir removes a directory that is empty and was last changed before the
// cutoff, so a directory created for a write that has not happened yet stays.
func (sw *sweep) emptyDir(dir string) {
	if info, err := os.Lstat(dir); err == nil && info.IsDir() && info.ModTime().Before(sw.cutoff) {
		_ = os.Remove(dir) // fails, harmlessly, while the directory has entries
	}
}

func (sw *sweep) file(p string) {
	if sw.keep[filepath.Clean(p)] {
		return
	}
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(sw.cutoff) {
		return
	}
	sw.remove(p)
}

// logDir removes a build-log directory when it holds only regular files,
// none written since the cutoff and none an in-flight deployment names.
func (sw *sweep) logDir(dir string) {
	if sw.keep[dir+string(filepath.Separator)] {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, infoErr := e.Info()
		if infoErr != nil || !e.Type().IsRegular() || !info.ModTime().Before(sw.cutoff) {
			return
		}
	}
	for _, e := range entries {
		sw.remove(filepath.Join(dir, e.Name()))
	}
	if len(entries) == 0 {
		sw.emptyDir(dir)
	} else {
		_ = os.Remove(dir)
	}
}

func (sw *sweep) remove(p string) {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		sw.errs = append(sw.errs, fmt.Errorf("remove %s: %w", p, err))
		return
	}
	sw.removed++
}

func (s Sweeper) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func isUUID(name string) bool {
	id, err := uuid.Parse(name)
	return err == nil && strings.EqualFold(id.String(), name)
}
