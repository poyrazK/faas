//go:build linux && metal

// spec: §4.8, §6.1

package fcvm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// TestMetalGuestInitAppExitAtStartup is the metal gate for the coding-agent
// eval of 2026-11-17. A FastAPI source build started `python main.py`, which
// exits 0. guest-init's PID 1 exited without draining the console, the
// kernel's panic dump cut "guest-init: app exited cleanly …" to
// "guest-init: app ", and the deploy failed as internal. On real Firecracker,
// with guest-init built from this tree: an app that exits 0 at startup fails
// as app_startup_crash with the clean-exit hint and guest-init's whole line;
// one that exits 1 still crash-loops into app_startup_crash without that
// hint; a serving app still becomes ready. Every VM is gone afterwards.
func TestMetalGuestInitAppExitAtStartup(t *testing.T) {
	kernel, _, _ := metalImages(t)
	guestInit := os.Getenv("FAAS_TEST_GUEST_INIT_BIN")
	if guestInit == "" {
		t.Skip("set FAAS_TEST_GUEST_INIT_BIN to a guest-init built from this tree")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	t.Cleanup(func() { leakcheck.AssertZero(t) })

	for _, tc := range []struct {
		name   string
		script string
		want   []string // in the problem detail
		reject []string // never in the problem detail
	}{
		{
			name:   "exit 0",
			script: "#!/bin/sh\necho 'guest-exit-fixture: main.py defined the app and returned'\nexit 0\n",
			want: []string{
				"exited before becoming ready",
				"guest-exit-fixture: main.py defined the app and returned",
				"guest-init: app exited cleanly (status 0, policy=on-failure); a clean exit is not restarted",
				"exited with status 0 (a clean exit is not restarted)",
			},
			reject: []string{"Kernel panic"},
		},
		{
			name:   "exit 1",
			script: "#!/bin/sh\necho 'guest-exit-fixture: boom'\nexit 1\n",
			want:   []string{"exited before becoming ready", "guest-exit-fixture: boom", "crash-looped after", "exit status 1"},
			reject: []string{"exited with status 0", "Kernel panic"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			base := filepath.Join(dir, "exit-base.ext4")
			if err := buildGuestInitScriptBase(base, guestInit, tc.script); err != nil {
				t.Fatalf("build base: %v", err)
			}
			layer := filepath.Join(dir, "exit-layer.ext4")
			if err := buildV6LayerExt4(layer); err != nil {
				t.Fatalf("build layer: %v", err)
			}
			m := newMetalManagerWithReadyTimeout(t, kernel, 90*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			started := time.Now()
			_, err := m.ColdBoot(ctx, ColdBootRequest{
				Instance: uuid.NewString(), Plan: "hobby", BaseKey: base, LayerKey: layer,
				VcpuCount: 1, MemSizeMiB: 128,
			})
			elapsed := time.Since(started)
			t.Logf("cold boot after %s: %v", elapsed, err)
			var problem *api.Problem
			if !errors.As(err, &problem) || problem.Code != api.CodeAppStartupCrash || problem.Status != 422 {
				t.Fatalf("cold boot error = %v, want a 422 %s problem", err, api.CodeAppStartupCrash)
			}
			for _, want := range tc.want {
				if !strings.Contains(problem.Detail, want) {
					t.Errorf("detail lacks %q:\n%s", want, problem.Detail)
				}
			}
			for _, reject := range tc.reject {
				if strings.Contains(problem.Detail, reject) {
					t.Errorf("detail has %q:\n%s", reject, problem.Detail)
				}
			}
			// A stopped guest ends readiness at once, not at its deadline.
			if elapsed > 60*time.Second {
				t.Errorf("cold boot took %s; readiness waited out its deadline", elapsed)
			}
			if m.LiveCount() != 0 {
				t.Errorf("LiveCount = %d after the failed boot, want 0", m.LiveCount())
			}
		})
	}

	t.Run("serves", func(t *testing.T) {
		dir := t.TempDir()
		base := filepath.Join(dir, "serve-base.ext4")
		if err := buildV6BaseExt4(base, ""); err != nil {
			t.Fatalf("build base: %v", err)
		}
		layer := filepath.Join(dir, "serve-layer.ext4")
		if err := buildV6LayerExt4(layer); err != nil {
			t.Fatalf("build layer: %v", err)
		}
		m := newMetalManagerWithReadyTimeout(t, kernel, 90*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		instance := uuid.NewString()
		if _, err := m.ColdBoot(ctx, ColdBootRequest{
			Instance: instance, Plan: "hobby", BaseKey: base, LayerKey: layer,
			VcpuCount: 1, MemSizeMiB: 128,
		}); err != nil {
			t.Fatalf("serving app cold boot: %v", err)
		}
		destroyCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if err := m.Destroy(destroyCtx, instance); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		if m.LiveCount() != 0 {
			t.Fatalf("LiveCount = %d after destroy, want 0", m.LiveCount())
		}
	})
}

// buildGuestInitScriptBase builds drive0 for guest-init running script as the
// app's entrypoint, with the default on-failure restart policy.
func buildGuestInitScriptBase(dst, guestInit, script string) error {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "guest-exit-skel-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	for _, sub := range []string{"bin", "sbin", "dev", "sys", "proc", "etc/faas", "usr/local/bin", "tmp", "var/tmp", "overlay"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}
	if err := bbCopyFile(guestInit, filepath.Join(work, "sbin", "init")); err != nil {
		return err
	}
	if err := bbCopyFile(bb, filepath.Join(work, "bin", "busybox")); err != nil {
		return err
	}
	for _, name := range []string{"bin/sh", "bin/ash"} {
		if err := os.Symlink("/bin/busybox", filepath.Join(work, name)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(work, "usr", "local", "bin", "app"), []byte(script), 0o755); err != nil {
		return err
	}
	appJSON := `{"entrypoint":["/usr/local/bin/app"],"port":8080}` + "\n"
	if err := os.WriteFile(filepath.Join(work, "etc", "faas", "app.json"), []byte(appJSON), 0o644); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := f.Truncate(128 << 20); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	mkfs := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", work, "-L", "faas-guest-exit", "-F", dst)
	mkfs.Stderr = os.Stderr
	if err := mkfs.Run(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o644)
}
