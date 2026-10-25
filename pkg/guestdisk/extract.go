package guestdisk

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
)

// DumpTree copies dir and everything beneath it out of the image into the
// session's scratch directory and returns the copy's root, named like dir's
// last component. The copy is untrusted: it holds whatever the guest put
// there, symlinks included (debugfs recreates them as symlinks), so callers
// must read it without following links, for example through os.Root and
// Lstat, and apply its own size caps. It disappears when the session closes.
// The copy cannot exceed the image's size. A missing dir is an error.
func (s *Session) DumpTree(ctx context.Context, dir string) (string, error) {
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
	out := filepath.Join(s.scratch, "out")
	if err := s.mkScratchDir(out); err != nil {
		return "", err
	}
	// rdump reports entries it cannot recreate (devices, ownership it may
	// not set) on stderr and carries on; those are exactly the entries a
	// copy of regular files drops anyway.
	res, err := s.run(ctx, s.ed.debugfs, []string{"-R", "rdump " + dir + " " + out, s.image}, nil, runLimits{output: 1 << 20, writeDirs: []string{out}})
	if err != nil {
		return "", fmt.Errorf("guestdisk: dump %s: %w", dir, err)
	}
	if res.exit != 0 {
		return "", fmt.Errorf("guestdisk: dump %s: debugfs exit %d: %s", dir, res.exit, firstLines(res.stderr, 4))
	}
	return filepath.Join(out, path.Base(dir)), nil
}
