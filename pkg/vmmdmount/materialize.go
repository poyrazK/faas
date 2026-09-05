package vmmdmount

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MaterializeParentExt4 copies a mounted parent tree into a shared staging
// directory.  Mounts created in vmmd's systemd namespace are not a reliable
// file-view boundary for the unprivileged imaged process, so the root-owned
// copy is the explicit handoff between the two daemons.
func MaterializeParentExt4(ctx context.Context, lowerdir, targetDir string) error {
	if lowerdir == "" || targetDir == "" {
		return fmt.Errorf("vmmdmount: materialize parent: empty path")
	}
	if err := rejectSymlinkOrEscape(lowerdir, filepath.Clean(MountRoot)+string(filepath.Separator), "lowerdir"); err != nil {
		return err
	}
	stagingPrefix := filepath.Clean(OverlayStagingRoot) + string(filepath.Separator)
	if err := rejectSymlinkOrEscape(targetDir, stagingPrefix, "target_dir"); err != nil {
		return err
	}
	if info, err := os.Stat(lowerdir); err != nil {
		return fmt.Errorf("vmmdmount: materialize parent: stat lowerdir: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("vmmdmount: materialize parent: lowerdir %q is not a directory", lowerdir)
	}
	if info, err := os.Stat(targetDir); err != nil {
		return fmt.Errorf("vmmdmount: materialize parent: stat target_dir: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("vmmdmount: materialize parent: target_dir %q is not a directory", targetDir)
	}

	return copyParentTree(ctx, lowerdir, targetDir)
}

// copyParentTree is kept separate so the content-vs-directory copy contract
// is testable without requiring a real loopback mount or privileged paths.
func copyParentTree(ctx context.Context, lowerdir, targetDir string) error {
	// Copy each child, including dotfiles, rather than lowerdir/. .
	// cp -a lowerdir/. targetDir also replaces targetDir's metadata with
	// the mounted root's uid/mode. That hands a root-owned 0755 directory
	// back to unprivileged imaged, which cannot apply its runtime delta.
	// Child metadata stays intact; the staging root stays caller-owned.
	entries, err := os.ReadDir(lowerdir)
	if err != nil {
		return fmt.Errorf("vmmdmount: materialize parent: list contents: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	args := []string{"-a", "--"}
	for _, entry := range entries {
		args = append(args, filepath.Join(lowerdir, entry.Name()))
	}
	args = append(args, targetDir)
	cmd := exec.CommandContext(ctx, "cp", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("vmmdmount: materialize parent: cp %q -> %q: %w (%s)",
			lowerdir, targetDir, err, strings.TrimSpace(string(out)))
	}
	return nil
}
