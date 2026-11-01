package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Pin the nearest existing directory and inspect every existing component.
// Missing components are created exclusively only after artifact preflight.
type artifactOutputDirectory struct {
	root    *os.Root
	anchor  *os.Root
	missing []string
	planned []string
}

// inspectArtifactOutputDirectory pins the absolute directory path. Symlinks
// in the part of path that already exists are the user's own (macOS /tmp and
// $TMPDIR, a symlinked home or checkout, an --out-dir that points at another
// disk), so they are resolved first. Everything the CLI owns stays a real
// directory: the components it creates, the project's .gregale state
// directory and what is below it, and the server-named task directories that
// callers open with openArtifactTaskDirectory.
func inspectArtifactOutputDirectory(path string) (*artifactOutputDirectory, error) {
	directory, err := walkArtifactOutputDirectory(resolveArtifactOutputPrefix(path))
	if err != nil {
		return nil, err
	}
	if planned := artifactPlannedDirectories(path, directory.missing); planned != nil {
		directory.planned = planned
	}
	return directory, nil
}

// resolveArtifactOutputPrefix resolves symlinks in the longest existing
// prefix of path that comes before any .gregale component. A path that
// cannot be resolved comes back unchanged, so the strict walk decides.
func resolveArtifactOutputPrefix(path string) string {
	separator := string(os.PathSeparator)
	user, managed := path, ""
	if i := strings.Index(path+separator, separator+projectContextDirName+separator); i >= 0 {
		user, managed = path[:i], path[i+1:]
		if user == filepath.VolumeName(path) {
			user += separator
		}
	}
	var suffix []string
	for prefix := user; ; {
		if resolved, err := filepath.EvalSymlinks(prefix); err == nil {
			parts := []string{resolved}
			for i := len(suffix) - 1; i >= 0; i-- {
				parts = append(parts, suffix[i])
			}
			return filepath.Join(append(parts, managed)...)
		}
		parent := filepath.Dir(prefix)
		if parent == prefix {
			return path
		}
		suffix = append(suffix, filepath.Base(prefix))
		prefix = parent
	}
}

// artifactPlannedDirectories spells the directories to create under the
// caller's path rather than its resolved form, or returns nil when the
// missing components are not the tail of path.
func artifactPlannedDirectories(path string, missing []string) []string {
	if len(missing) == 0 {
		return nil
	}
	prefix := path
	for i := len(missing) - 1; i >= 0; i-- {
		if filepath.Base(prefix) != missing[i] {
			return nil
		}
		prefix = filepath.Dir(prefix)
	}
	planned := make([]string, 0, len(missing))
	for _, name := range missing {
		prefix = filepath.Join(prefix, name)
		planned = append(planned, prefix)
	}
	return planned
}

func walkArtifactOutputDirectory(path string) (*artifactOutputDirectory, error) {
	base := filepath.VolumeName(path) + string(os.PathSeparator)
	parts := strings.Split(strings.TrimPrefix(path, base), string(os.PathSeparator))
	current, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	walked := base
	for i, part := range parts {
		if part == "" {
			continue
		}
		_, err := current.Lstat(part)
		if os.IsNotExist(err) {
			result := &artifactOutputDirectory{anchor: current, missing: parts[i:]}
			for _, name := range result.missing {
				walked = filepath.Join(walked, name)
				result.planned = append(result.planned, walked)
			}
			return result, nil
		}
		if err != nil {
			_ = current.Close()
			return nil, err
		}
		next, err := openArtifactTaskDirectory(current, part)
		if err != nil {
			_ = current.Close()
			return nil, fmt.Errorf("output path components must be real directories, not files or symlinks")
		}
		_ = current.Close()
		current = next
		walked = filepath.Join(walked, part)
	}
	return &artifactOutputDirectory{root: current, anchor: current}, nil
}

func (d *artifactOutputDirectory) Close() {
	if d.root != nil && d.root != d.anchor {
		_ = d.root.Close()
	}
	_ = d.anchor.Close()
}

func (d *artifactOutputDirectory) create(ctx context.Context) error {
	if d.root != nil {
		return nil
	}
	current := d.anchor
	for _, part := range d.missing {
		if ctx.Err() != nil {
			if current != d.anchor {
				_ = current.Close()
			}
			return ctx.Err()
		}
		if err := current.Mkdir(part, 0750); err != nil {
			if current != d.anchor {
				_ = current.Close()
			}
			return fmt.Errorf("could not create output directory; a destination may have appeared or permissions may prevent creation; no existing directory was adopted")
		}
		next, err := openArtifactTaskDirectory(current, part)
		if current != d.anchor {
			_ = current.Close()
		}
		if err != nil {
			return err
		}
		current = next
	}
	d.root = current
	return nil
}
