//go:build linux

package builderd

import (
	"context"
	"os/exec"
)

// runMount is a thin wrapper around `mount` for use by tests that need to
// re-mount a drive1 image CreateBuildDrive1 has already produced (issue
// #54's end-to-end sha256 check). Mirrors the call shape inside
// CreateBuildDrive1 so a behaviour drift in one place breaks the test.
func runMount(ctx context.Context, opts ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "mount", opts...).CombinedOutput()
}

// runUmount is the corresponding `umount` wrapper. Best-effort: tests
// `defer` this and ignore the error so a flaky umount doesn't mask the
// real assertion failure.
func runUmount(target string) ([]byte, error) {
	return exec.Command("umount", target).CombinedOutput()
}
