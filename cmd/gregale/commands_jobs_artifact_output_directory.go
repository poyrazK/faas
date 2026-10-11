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

func inspectArtifactOutputDirectory(path string) (*artifactOutputDirectory, error) {
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
			current.Close()
			return nil, err
		}
		next, err := openArtifactTaskDirectory(current, part)
		if err != nil {
			current.Close()
			return nil, fmt.Errorf("output path components must be real directories, not files or symlinks")
		}
		current.Close()
		current = next
		walked = filepath.Join(walked, part)
	}
	return &artifactOutputDirectory{root: current, anchor: current}, nil
}

func (d *artifactOutputDirectory) Close() {
	if d.root != nil && d.root != d.anchor {
		d.root.Close()
	}
	d.anchor.Close()
}

func (d *artifactOutputDirectory) create(ctx context.Context) error {
	if d.root != nil {
		return nil
	}
	current := d.anchor
	for _, part := range d.missing {
		if ctx.Err() != nil {
			if current != d.anchor {
				current.Close()
			}
			return ctx.Err()
		}
		if err := current.Mkdir(part, 0750); err != nil {
			if current != d.anchor {
				current.Close()
			}
			return fmt.Errorf("could not create output directory; a destination may have appeared or permissions may prevent creation; no existing directory was adopted")
		}
		next, err := openArtifactTaskDirectory(current, part)
		if current != d.anchor {
			current.Close()
		}
		if err != nil {
			return err
		}
		current = next
	}
	d.root = current
	return nil
}
