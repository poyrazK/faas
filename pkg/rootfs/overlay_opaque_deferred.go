package rootfs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/overlaymetadata"
)

// deferredOpaqueMarker names the empty file applyOverlayOpaque leaves in a
// directory whose trusted.overlay.opaque xattr the kernel refused (imaged has
// no CAP_SYS_ADMIN). applyDeferredOverlayOpacity sets the attribute on that
// directory inside the finished ext4 image and removes the marker there, so
// it never reaches a guest. Image entries with this name are refused.
const deferredOpaqueMarker = ".faas-overlay-opaque"

// errReservedOverlayMarker refuses an image entry named like the marker.
var errReservedOverlayMarker = errors.New("rootfs: image entry uses the reserved name " + deferredOpaqueMarker)

func markDeferredOverlayOpaque(dir string) error {
	// dir is a build staging directory the daemon created.
	f, err := os.OpenFile(filepath.Join(dir, deferredOpaqueMarker), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:forbidigo
	if err != nil {
		return err
	}
	return f.Close()
}

// deferredOpaqueDirs lists the image paths (rooted at "/", slash separated)
// of the staging directories that carry a deferred opacity marker.
func deferredOpaqueDirs(staging string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(staging, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() != deferredOpaqueMarker || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(staging, filepath.Dir(path))
		if err != nil {
			return err
		}
		dirs = append(dirs, "/"+strings.TrimPrefix(filepath.ToSlash(rel), "."))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("rootfs: find deferred overlay opacity: %w", err)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// debugfsPathArg quotes an image path for a debugfs command file. debugfs
// splits on whitespace and honours double quotes; a path that would need
// more than that is refused rather than guessed at.
func debugfsPathArg(path string) (string, error) {
	for _, r := range path {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("rootfs: opaque directory %q cannot be written to the image", path)
		}
	}
	return `"` + strings.ReplaceAll(path, "//", "/") + `"`, nil
}

// deferredOpacityScript is the debugfs command file that marks each
// directory opaque and removes its marker.
func deferredOpacityScript(dirs []string) ([]byte, error) {
	var script bytes.Buffer
	for _, dir := range dirs {
		dirArg, err := debugfsPathArg(dir)
		if err != nil {
			return nil, err
		}
		markerArg, err := debugfsPathArg(strings.TrimSuffix(dir, "/") + "/" + deferredOpaqueMarker)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&script, "ea_set %s %s y\n", dirArg, overlaymetadata.RootOpaqueXattr)
		fmt.Fprintf(&script, "rm %s\n", markerArg)
	}
	return script.Bytes(), nil
}

// combinedOutputRunner is the Runner that also returns a command's combined output.
// debugfs reports a failed request on stderr and still exits 0, so the
// opacity pass reads its output instead of trusting the status.
type combinedOutputRunner interface {
	CombinedOutput(ctx context.Context, argv []string) ([]byte, error)
}

// debugfsOutputError returns the first line of debugfs output that is not
// its banner or an echoed request: each such line is a failed request.
func debugfsOutputError(out []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "debugfs ") || strings.HasPrefix(line, "debugfs:") {
			continue
		}
		return fmt.Errorf("debugfs: %s", line)
	}
	return nil
}

// applyDeferredOverlayOpacity writes trusted.overlay.opaque into image for
// every staging directory applyOverlayOpaque could not mark itself.
func (b *Builder) applyDeferredOverlayOpacity(ctx context.Context, staging, image string) error {
	dirs, err := deferredOpaqueDirs(staging)
	if err != nil || len(dirs) == 0 {
		return err
	}
	script, err := deferredOpacityScript(dirs)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "faas-opacity-*.debugfs")
	if err != nil {
		return fmt.Errorf("rootfs: deferred overlay opacity script: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(script); err != nil {
		_ = f.Close()
		return fmt.Errorf("rootfs: deferred overlay opacity script: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("rootfs: deferred overlay opacity script: %w", err)
	}
	argv := []string{"debugfs", "-w", "-f", f.Name(), image}
	if runner, ok := b.run.(combinedOutputRunner); ok {
		out, err := runner.CombinedOutput(ctx, argv)
		if err == nil {
			err = debugfsOutputError(out)
		}
		if err != nil {
			return fmt.Errorf("rootfs: set deferred overlay opacity: %w", err)
		}
		return nil
	}
	if err := b.run.Run(ctx, argv); err != nil {
		return fmt.Errorf("rootfs: set deferred overlay opacity: %w", err)
	}
	return nil
}
