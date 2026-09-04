package imaged

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ValidateBaseArtifact checks the small boot/runtime contract that must be
// present in a staged ext4 image. The check intentionally runs against the
// ext4 itself, rather than the OCI image, because an incomplete builder,
// failed mkfs copy, or stale sidecar can otherwise make an apparently valid
// digest point at a bootable-looking but unusable artifact.
//
// debugfs is used read-only and without mounting the image. That keeps the
// check compatible with imaged's unprivileged systemd sandbox.
func ValidateBaseArtifact(ctx context.Context, imagePath string, required []string) error {
	if strings.TrimSpace(imagePath) == "" {
		return fmt.Errorf("empty ext4 image path")
	}
	if len(required) == 0 {
		return fmt.Errorf("no required paths configured")
	}
	for _, path := range required {
		if !validDebugFSPath(path) {
			return fmt.Errorf("invalid required ext4 path %q", path)
		}
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return fmt.Errorf("debugfs is required for base validation: %w", err)
	}
	for _, path := range required {
		cmd := exec.CommandContext(ctx, "debugfs", "-R", "stat "+path, imagePath)
		output, err := cmd.CombinedOutput()
		outputText := strings.TrimSpace(string(output))
		// debugfs has historically returned success for some failed -R
		// commands, so check its diagnostic text and the inode marker too.
		if err != nil || strings.Contains(outputText, "File not found") || !strings.Contains(outputText, "Inode:") {
			if err == nil {
				err = fmt.Errorf("debugfs did not report an inode")
			}
			return fmt.Errorf("required path %s missing from %s: %w (%s)", path, imagePath, err, outputText)
		}
	}
	return nil
}

func validDebugFSPath(path string) bool {
	if path == "" || path[0] != '/' || strings.IndexByte(path, 0) >= 0 {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '/' || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func requiredBaseArtifactPaths(baseKey string) []string {
	switch {
	case strings.Contains(baseKey, "runner-builder"):
		return []string{"/sbin/init", "/usr/local/bin/faas-guest-init", "/usr/local/bin/railpack", "/usr/local/bin/buildctl", "/usr/local/bin/runc", "/bin/bash"}
	case strings.Contains(baseKey, "runner-node"):
		return []string{"/sbin/init", "/usr/local/bin/node", "/etc/passwd"}
	case strings.Contains(baseKey, "runner-python"):
		return []string{"/sbin/init", "/usr/local/bin/python3", "/etc/passwd"}
	case strings.Contains(baseKey, "runner-go"):
		return []string{"/sbin/init", "/usr/local/go/bin/go", "/etc/passwd"}
	case strings.Contains(baseKey, "base-amd64") || strings.Contains(baseKey, "base-arm64"):
		return []string{"/sbin/init", "/bin/busybox", "/bin/sh", "/etc/passwd"}
	default:
		return []string{"/sbin/init", "/etc/passwd"}
	}
}
