//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Guest device layout (spec §4.6): drive0 (vda) is the shared read-only base and
// the kernel root; drive1 (vdb) is the per-app writable layer. guest-init mounts
// vdb, builds an overlay with the base as the read-only lower and the layer as
// the writable upper, pivots into it, then execs the app.
const (
	layerDevice = "/dev/vdb"
	layerMount  = "/overlay"
	newRoot     = "/overlay/merged"
)

// bootMode is which branch of the build (BuildManifest present) vs app
// (AppManifest present) guest-init took. decideMode is split out so unit
// tests can drive it with testing/fstest.MapFS.
type bootMode int

const (
	modeApp bootMode = iota
	modeBuild
)

// main is guest PID 1. Any fatal error here panics the VM (panic=1 in boot args
// reboots it), which schedd observes as a failed wake.
func main() {
	if err := boot(); err != nil {
		fmt.Fprintf(os.Stderr, "guest-init: %v\n", err)
		os.Exit(1)
	}
}

func boot() error {
	if err := mountBasics(); err != nil {
		return fmt.Errorf("mount basics: %w", err)
	}
	if err := assembleOverlay(); err != nil {
		return fmt.Errorf("assemble overlay: %w", err)
	}
	if err := pivotInto(newRoot); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}

	mode, buildManifest, err := decideMode(os.DirFS("/"))
	if err != nil {
		return err
	}
	if mode == modeBuild {
		return runBuild(buildManifest)
	}

	f, err := os.Open(api.AppManifestPath)
	if err != nil {
		return fmt.Errorf("open manifest: %w", err)
	}
	manifest, err := api.ReadManifest(f)
	_ = f.Close()
	if err != nil {
		return err
	}

	// G2: read /etc/faas/secrets.env (unsealed JSON, written by vmmd at
	// wake time) and stash the entry count on the supervisor via a small
	// closure so runAppWithSecrets can pull them. A missing or malformed file is
	// not fatal — the app runs without env secrets (consistent with
	// the quota=0 path).
	secrets, secErr := loadSecrets(slog.Default())
	if secErr != nil {
		// Don't surface the value, just the fact that the file failed.
		slog.Default().Warn("secrets.env could not be loaded; proceeding without secrets", "err_kind", errorKind(secErr))
	}

	sup := Supervisor{
		Max:   MaxRestarts,
		Start: func() error { return runAppWithSecrets(manifest, secrets) },
		OnCrash: func(attempt int, err error) {
			fmt.Fprintf(os.Stderr, "guest-init: app crashed (restart %d/%d): %v\n", attempt, MaxRestarts, err)
		},
	}
	return sup.Run()
}

// runAppWithSecrets is the secrets-aware entrypoint — same execve path as
// runApp but with the envelope layered over the manifest's env. Empty/nil
// secrets short-circuits to the un-secrets path (i.e. BuildEnv).
func runAppWithSecrets(m api.AppManifest, secrets map[string]string) error {
	argv := m.Entrypoint
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.EffectiveWorkingDir()
	cmd.Env = BuildEnvWithSecrets(os.Environ(), m, secrets)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if uid := lookupUID(m.EffectiveUser()); uid > 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)},
		}
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %v: %w", argv, err)
	}
	return nil
}

// errorKind collapses an error chain to a stable string suitable for a
// slog attribute. We never log the raw err (it could carry a malformed
// secrets file path or partial bytes), only its structural class.
func errorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fs.ErrPermission):
		return "permission"
	case isNotExist(err):
		return "absent"
	default:
		// JSON unmarshal failure or read failure other than ENOENT.
		if strings.HasPrefix(err.Error(), "secrets: parse") {
			return "parse"
		}
		if strings.HasPrefix(err.Error(), "secrets: read") {
			return "read"
		}
		return "other"
	}
}

// decideMode picks the boot branch by looking at which manifest file exists.
// The build manifest takes precedence if both are present (defensive —
// shouldn't happen in practice because base images carry at most one).
//
// Split out from boot() so unit tests can drive it with testing/fstest.MapFS
// instead of touching the real root fs. The path passed to fs.ReadFile must
// be RELATIVE (no leading "/") — fs.FS rejects absolute paths, and the real
// os.DirFS("/") used at boot happily accepts the relative form on Linux.
func decideMode(fsys fs.FS) (bootMode, api.BuildManifest, error) {
	if data, err := fs.ReadFile(fsys, "etc/faas/build.json"); err == nil {
		var m api.BuildManifest
		if jErr := json.Unmarshal(data, &m); jErr == nil {
			return modeBuild, m, nil
		}
	}
	return modeApp, api.BuildManifest{}, nil
}

// runBuild is the builder-VM path (M6). It extracts the source tarball,
// invokes the chosen build engine (Railpack / buildctl / auto), writes
// build-done.json with the outcome, and powers off. poweroff is what makes
// firecracker exit cleanly with the build's exit code (vmmd's
// DestroyResponse.exit_code on the wire — see pkg/vmmdgrpc/server.go).
func runBuild(m api.BuildManifest) error {
	if m.Workdir == "" {
		m.Workdir = "/build/src"
	}
	if m.OutDir == "" {
		m.OutDir = "/build/out"
	}
	if err := os.MkdirAll(m.Workdir, 0o755); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mkdir workdir: %w", err), "")
	}
	if err := os.MkdirAll(m.OutDir, 0o755); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mkdir outdir: %w", err), "")
	}

	// 1. Extract source tarball.
	if m.SourceTarPath != "" {
		if out, err := exec.Command("tar", "-xaf", m.SourceTarPath, "-C", m.Workdir).CombinedOutput(); err != nil {
			return writeAndPoweroff(m, fmt.Errorf("tar extract: %w (%s)", err, out), "")
		}
	}

	// 2. Pick the build command.
	var argv []string
	switch m.Framework {
	case api.FrameworkDockerfile:
		argv = []string{
			"buildctl", "build",
			"--frontend", "dockerfile",
			"--local", "context=" + m.Workdir,
			"--local", "dockerfile=" + m.Workdir,
			"--output", "type=oci,dest=" + m.OutDir + "/image.tar",
		}
	default:
		// railpack with --plan auto|node|python
		plan := "auto"
		switch m.Framework {
		case api.FrameworkRailpackNode:
			plan = "node"
		case api.FrameworkRailpackPython:
			plan = "python"
		}
		argv = []string{"railpack", "build", m.OutDir, "--plan", plan}
	}

	// 3. Run with a wall-clock timeout (we already get OOM protection from
	//    cgroup v2 memory.max on the Firecracker config — see spec §11).
	timeoutSec := m.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = api.BuildTimeoutSeconds
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = m.Workdir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()

	return writeAndPoweroff(m, err, tailOf(buf.Bytes(), m.LogTailBytes))
}

// classify maps an in-VM exit code to a builderd FailureClass. The vocabulary
// here matches the canonical names parsed by builderd's ProcessOne
// (FailureUserError / FailureInfra / FailureOOM / FailureTimeout).
func classify(exitCode int) string {
	switch exitCode {
	case 137:
		return "FailureOOM"
	case 124:
		return "FailureTimeout"
	case 0:
		return ""
	default:
		return "FailureUserError"
	}
}

// tailOf returns the last n bytes of data (or all of it if shorter). Used to
// truncate the build log so build-done.json stays small.
func tailOf(data []byte, n int) string {
	if n <= 0 || len(data) <= n {
		return string(data)
	}
	return string(data[len(data)-n:])
}

// writeAndPoweroff writes /etc/faas/build-done.json (vmmd's Destroy loopback-
// mounts the chroot drive1 to copy it out) and powers off the VM. Any
// failure here is logged but doesn't prevent the poweroff — vmmd will
// surface a fallback exit-code classification via the watch-dog capture.
func writeAndPoweroff(m api.BuildManifest, runErr error, logTail string) error {
	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	fc := classify(exitCode)
	done := api.BuildDone{
		SchemaVersion: 1,
		BuildID:       m.BuildID,
		ExitCode:      exitCode,
		OCIImagePath:  m.OutDir + "/image.tar",
		LogTail:       logTail,
		FailureClass:  fc,
	}
	if data, mErr := json.Marshal(done); mErr == nil {
		_ = os.WriteFile(api.BuildDonePath, data, 0o644)
	} else {
		fmt.Fprintf(os.Stderr, "guest-init: marshal build-done: %v\n", mErr)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "guest-init: build failed: %v\n", runErr)
	}
	// poweroff -f so vmmd's Destroy sees the exit code via firecracker's
	// natural exit. exec.CommandContext's timeout doesn't trigger poweroff —
	// we always get here.
	if err := exec.Command("poweroff", "-f").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "guest-init: poweroff: %v\n", err)
	}
	// Surface as a guest-init error so the cmd survives long enough for
	// firecracker to capture the exit (poweroff schedules immediate halt).
	if runErr != nil {
		return runErr
	}
	return nil
}

// classifyTail is a debug-only helper used to shorten long log tails in
// writeBuildDone. Kept as a package-private function so tests can import it
// via an internal-test file (not used elsewhere; would be dead code on darwin).
var _ = strings.HasPrefix

// mountBasics mounts the pseudo-filesystems every app expects.
func mountBasics() error {
	type m struct{ src, dst, fs string }
	for _, mnt := range []m{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"tmpfs", "/tmp", "tmpfs"},
		{"devtmpfs", "/dev", "devtmpfs"},
	} {
		_ = os.MkdirAll(mnt.dst, 0o755)
		if err := syscall.Mount(mnt.src, mnt.dst, mnt.fs, 0, ""); err != nil {
			// /dev may already be a devtmpfs from the kernel; tolerate EBUSY.
			if mnt.dst != "/dev" {
				return fmt.Errorf("mount %s: %w", mnt.dst, err)
			}
		}
	}
	return nil
}

// assembleOverlay mounts the app layer and stacks it over the read-only base.
func assembleOverlay() error {
	if err := os.MkdirAll(layerMount, 0o755); err != nil {
		return err
	}
	if err := syscall.Mount(layerDevice, layerMount, "ext4", 0, ""); err != nil {
		return fmt.Errorf("mount layer %s: %w", layerDevice, err)
	}
	for _, d := range []string{"upper", "work", "merged"} {
		if err := os.MkdirAll(layerMount+"/"+d, 0o755); err != nil {
			return err
		}
	}
	opts := "lowerdir=/,upperdir=" + layerMount + "/upper,workdir=" + layerMount + "/work"
	if err := syscall.Mount("overlay", newRoot, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}
	return nil
}

// pivotInto makes root the new root filesystem.
func pivotInto(root string) error {
	if err := os.MkdirAll(root+"/oldroot", 0o755); err != nil {
		return err
	}
	if err := syscall.PivotRoot(root, root+"/oldroot"); err != nil {
		return err
	}
	if err := syscall.Chdir("/"); err != nil {
		return err
	}
	// Detach the old root lazily.
	_ = syscall.Unmount("/oldroot", syscall.MNT_DETACH)
	return nil
}

// lookupUID resolves the app user name to a uid. The runner images create the
// app user at DefaultAppUID; unknown users fall back to that.
func lookupUID(user string) int {
	if user == api.DefaultAppUser {
		return api.DefaultAppUID
	}
	return api.DefaultAppUID
}
