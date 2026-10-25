package guestdisk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// DumpBudget bounds what DumpFiles may write on the host.
type DumpBudget struct {
	// FileBytes skips any regular file larger than this.
	FileBytes int64
	// TotalBytes bounds the apparent size of every file dumped together. A
	// sparse file or a hard link takes no room on the drive but is written
	// at full size, so apparent size is what counts.
	TotalBytes int64
	// Entries bounds the entries visited, directories included.
	Entries int
}

// ErrDumpBudget reports a tree larger than its DumpBudget allows. Nothing is
// written when the budget fails: it is checked against the listing first.
var ErrDumpBudget = errors.New("guestdisk: tree exceeds its dump budget")

// DumpFiles copies the directories and regular files beneath dir out of the
// image into dest, an existing empty directory, and returns the copy's root
// (dest joined with dir's last component). Symlinks, devices, FIFOs and
// sockets are skipped rather than recreated, as is a regular file over
// b.FileBytes or one whose name cannot be written on a script line. The
// whole tree is planned from a listing and held to b before anything is
// written. The tool reaches dest through an inherited descriptor, so dest's
// ancestors need not admit the image owner, and it may write nowhere else.
func (s *Session) DumpFiles(ctx context.Context, dir, dest string, b DumpBudget) (string, error) {
	if err := CheckPath(dir); err != nil {
		return "", err
	}
	got, err := s.Inspect(ctx, components(dir), nil)
	if err != nil {
		return "", err
	}
	for _, c := range components(dir) {
		if got[c].Kind != Directory {
			return "", &PathError{Path: dir, Err: fmt.Errorf("%s is a %s: %w", c, got[c].Kind, ErrNotDirectory)}
		}
	}
	dirs, files, err := s.planDump(ctx, dir, b)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dest) {
		return "", fmt.Errorf("guestdisk: dump destination %q is not absolute", dest)
	}
	d, err := os.OpenFile(dest, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("guestdisk: open dump destination: %w", err)
	}
	defer func() { _ = d.Close() }()
	if names, err := d.Readdirnames(1); len(names) > 0 || err == nil {
		return "", fmt.Errorf("guestdisk: dump destination %s is not empty", dest)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return "", fmt.Errorf("guestdisk: open dump destination: %w", err)
	}
	defer func() { _ = root.Close() }()
	base := path.Base(dir)
	// vmmd lays the directories out; the tool only writes regular files
	// into them. Nothing else writes dest, which is fresh and private.
	for _, rel := range append([]string{base}, dirs...) {
		if rel != base {
			rel = base + "/" + rel
		}
		if err := root.Mkdir(rel, 0o700); err != nil {
			return "", fmt.Errorf("guestdisk: dump destination: %w", err)
		}
		if os.Geteuid() == 0 {
			if err := root.Chown(rel, s.uid, s.gid); err != nil {
				return "", fmt.Errorf("guestdisk: dump destination: %w", err)
			}
		}
	}
	if len(files) == 0 {
		return filepath.Join(dest, base), nil
	}
	if os.Geteuid() == 0 {
		if err := d.Chown(s.uid, s.gid); err != nil {
			return "", fmt.Errorf("guestdisk: hand dump destination to %d:%d: %w", s.uid, s.gid, err)
		}
	}
	target := dest
	lim := runLimits{output: 1 << 20, writeDirs: []string{dest}}
	if s.ed.cfg.Helper != "" {
		// The child's first extra descriptor is fd 3.
		target = "/proc/self/fd/3"
		lim.writeDirs = []string{target}
		lim.extraFiles = []*os.File{d}
	}
	var script bytes.Buffer
	for _, f := range files {
		fmt.Fprintf(&script, "dump %s %s\n", Quote(dir+"/"+f.rel), Quote(target+"/"+base+"/"+f.rel))
	}
	res, err := s.run(ctx, s.ed.debugfs, []string{"-f", "-", s.image}, script.Bytes(), lim)
	if err != nil {
		return "", fmt.Errorf("guestdisk: dump %s: %w", dir, err)
	}
	if problems := s.unexpectedStderr(res.stderr, nil); len(problems) > 0 {
		return "", fmt.Errorf("guestdisk: dump %s: %v", dir, problems)
	}
	for _, f := range files {
		info, err := root.Lstat(base + "/" + f.rel)
		if err != nil || !info.Mode().IsRegular() || info.Size() != f.size {
			return "", &PathError{Path: dir + "/" + f.rel, Err: errors.New("dump did not take effect")}
		}
	}
	return filepath.Join(dest, base), nil
}

type dumpFile struct {
	rel  string
	size int64
}

// planDump lists dir breadth first and returns the directories to create and
// the regular files to dump, relative to dir, held to the budget.
func (s *Session) planDump(ctx context.Context, dir string, b DumpBudget) ([]string, []dumpFile, error) {
	type pending struct {
		rel   string
		depth int
	}
	var dirs []string
	var files []dumpFile
	frontier := []pending{{}}
	entries := 0
	var total int64
	for len(frontier) > 0 {
		paths := make([]string, len(frontier))
		for i, p := range frontier {
			paths[i] = path.Join(dir, p.rel)
		}
		listing, err := s.List(ctx, paths, b.Entries-entries+1)
		if err != nil {
			if errors.Is(err, errListTooLong) {
				return nil, nil, fmt.Errorf("%w: more than %d entries", ErrDumpBudget, b.Entries)
			}
			return nil, nil, err
		}
		var next []pending
		for i, p := range frontier {
			for _, e := range listing[paths[i]] {
				if entries++; entries > b.Entries {
					return nil, nil, fmt.Errorf("%w: more than %d entries", ErrDumpBudget, b.Entries)
				}
				rel := path.Join(p.rel, e.Name)
				if checkQuotable(rel) != nil {
					continue // not expressible on a script line; dropped like a link
				}
				switch e.Kind {
				case Directory:
					if p.depth+1 > maxRemoveDepth {
						return nil, nil, fmt.Errorf("%w: nested deeper than %d", ErrDumpBudget, maxRemoveDepth)
					}
					dirs = append(dirs, rel)
					next = append(next, pending{rel: rel, depth: p.depth + 1})
				case Regular:
					if b.FileBytes > 0 && e.Size > b.FileBytes {
						continue
					}
					if total += e.Size; total > b.TotalBytes {
						return nil, nil, fmt.Errorf("%w: more than %d bytes", ErrDumpBudget, b.TotalBytes)
					}
					files = append(files, dumpFile{rel: rel, size: e.Size})
				}
			}
		}
		frontier = next
	}
	return dirs, files, nil
}
