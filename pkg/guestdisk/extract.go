package guestdisk

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// DumpTree copies dir and everything beneath it out of the image into dest,
// an existing empty directory, and returns the copy's root (dest joined with
// dir's last component). The copy is untrusted: it holds whatever the guest
// put there, symlinks included (debugfs recreates them as symlinks), so the
// caller must read it without following links and apply its own size caps.
// The tool reaches dest through an inherited descriptor, so dest's ancestors
// need not admit the image owner; the sandbox may write nowhere else. Each
// file is bounded by the image's size. A missing dir is an error.
func (s *Session) DumpTree(ctx context.Context, dir, dest string) (string, error) {
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
	// rdump reports entries it cannot recreate (devices, ownership it may
	// not set) on stderr and carries on; those are exactly the entries a
	// copy of regular files drops anyway.
	res, err := s.run(ctx, s.ed.debugfs, []string{"-R", "rdump " + dir + " " + target, s.image}, nil, lim)
	if err != nil {
		return "", fmt.Errorf("guestdisk: dump %s: %w", dir, err)
	}
	if res.exit != 0 {
		return "", fmt.Errorf("guestdisk: dump %s: debugfs exit %d: %s", dir, res.exit, firstLines(res.stderr, 4))
	}
	return filepath.Join(dest, path.Base(dir)), nil
}
