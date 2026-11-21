//go:build metal && linux

package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// A root-only, bounded barrier in a designated acceptance fixture permits the
// harness to SIGKILL the actual daemon at a durable protocol boundary. It is
// compiled out of production; no caller payload or public API selects it.
func executionTemplateCaptureCheckpoint(ctx context.Context, stage, instance, memory string) error {
	root := os.Getenv("FAAS_TEST_EXECUTION_TEMPLATE_BARRIER_ROOT")
	if root == "" {
		return nil
	}
	base := os.Getenv("FAAS_TEST_RUN_FIXTURE_ROOT")
	relative, err := filepath.Rel(base, root)
	if os.Getenv("GREGALE_CI_NATIVE_ACCEPTANCE") != "1" || !filepath.IsAbs(base) || !filepath.IsAbs(root) || filepath.Clean(root) != root || err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
		return errors.New("execution template barrier: designated private acceptance fixture required")
	}
	if _, err := os.Stat("/etc/faas/builder-acceptance-host"); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0o700 || stat.Uid != 0 {
		return errors.New("execution template barrier: unsafe root")
	}
	armed, err := os.ReadFile(filepath.Join(root, "armed")) //nolint:forbidigo // Root-only acceptance fixture, compiled out of production.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(armed) > 64 {
		return errors.New("execution template barrier: invalid stage")
	}
	if strings.TrimSpace(string(armed)) != stage {
		return nil
	}
	if err := writeNativeJournalValue(filepath.Join(root, "reached.json"), struct{ Stage, Instance, Memory string }{stage, instance, memory}); err != nil {
		return err
	}
	for {
		if _, err := os.Lstat(filepath.Join(root, "release")); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
