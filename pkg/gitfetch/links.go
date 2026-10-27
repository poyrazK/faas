package gitfetch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// pendingLink is an archive link waiting to be replaced by a copy of the entry
// it names. Both paths are archive-relative with the codeload wrapper removed.
type pendingLink struct {
	name, target string
}

// linkCopier dereferences links after extraction (ADR-20261028090000). A
// repository's in-tree links (a shared LICENSE, CLAUDE.md -> AGENTS.md, a
// monorepo package pointing at a sibling) used to fail every push with
// "entry type 50 not allowed". The fetched tree is re-packed for the build,
// so each link becomes a copy of its target, as `gregale deploy` does
// locally. No link is ever created on this host, and copies count against
// the same per-file, entry and total byte caps as extracted files.
type linkCopier struct {
	dst           string
	links         map[string]string
	done          map[string]bool
	visiting      map[string]bool
	total         *int64
	maxTotalBytes int64
	maxFileBytes  int64
	entries       *int
	maxEntries    int
}

func materializeLinks(dst string, pending []pendingLink, total *int64, maxTotalBytes, maxFileBytes int64, entries *int, maxEntries int) error {
	c := &linkCopier{dst: dst, links: make(map[string]string, len(pending)), done: map[string]bool{}, visiting: map[string]bool{},
		total: total, maxTotalBytes: maxTotalBytes, maxFileBytes: maxFileBytes, entries: entries, maxEntries: maxEntries}
	for _, link := range pending {
		if _, dup := c.links[link.name]; dup {
			return fmt.Errorf("duplicate link %q: %w", link.name, ErrBadArchive)
		}
		c.links[link.name] = link.target
	}
	for _, link := range pending {
		if err := c.materialize(link.name); err != nil {
			return err
		}
	}
	return nil
}

func underPath(p, prefix string) bool {
	return prefix == "." || p == prefix || strings.HasPrefix(p, prefix+"/")
}

func (c *linkCopier) materialize(name string) error {
	if c.done[name] || c.visiting[name] {
		// A cycle of links names nothing; it is left out, as Git leaves a
		// dangling link unresolved.
		return nil
	}
	c.visiting[name] = true
	defer delete(c.visiting, name)
	target := c.links[name]
	// Materialize what the copy reads first: a link on the target's path
	// ("a -> b/c" where b is a link) and every link inside a target directory.
	for other := range c.links {
		reads := underPath(other, target) || underPath(target, other)
		if other != name && reads && !underPath(other, name) {
			if err := c.materialize(other); err != nil {
				return err
			}
		}
	}
	c.done[name] = true
	if underPath(name, target) {
		// A link to its own ancestor would copy itself forever.
		return nil
	}
	src := filepath.Join(c.dst, filepath.FromSlash(target))
	out := filepath.Join(c.dst, filepath.FromSlash(name))
	if !filepath.IsLocal(filepath.FromSlash(target)) || !filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("invalid link %q -> %q: %w", name, target, ErrBadArchive)
	}
	info, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat link target %q: %w", target, err)
	}
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("link %q collides with another entry: %w", name, ErrBadArchive)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("mkdir parent %q: %w", filepath.Dir(out), err)
	}
	if !info.IsDir() {
		return c.copyFile(src, out, info)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(out, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		entryInfo, err := d.Info()
		if err != nil {
			return err
		}
		return c.copyFile(p, dest, entryInfo)
	})
}

func (c *linkCopier) copyFile(src, dst string, info fs.FileInfo) error {
	if !info.Mode().IsRegular() {
		// Extraction only ever creates directories and regular files.
		return fmt.Errorf("link target %q is not a regular file: %w", src, ErrBadArchive)
	}
	*c.entries++
	if c.maxEntries > 0 && *c.entries > c.maxEntries {
		return fmt.Errorf("too many files (>%d): %w", c.maxEntries, ErrBadArchive)
	}
	in, err := os.Open(src) //nolint:gosec,forbidigo // src is a regular file this extraction wrote under its own temp dir; links were never created
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return writeOneFile(dst, in, info.Size(), c.maxFileBytes, c.total, c.maxTotalBytes, info.Mode())
}
