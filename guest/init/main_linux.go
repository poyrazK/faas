//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	// ADR-022: bind the AF_VSOCK resume listener BEFORE the supervisor starts
	// the app, so a post-restore dial from vmmd can never race the listener
	// coming up. We tolerate a bind failure (e.g. AF_VSOCK not compiled into
	// the guest kernel) on cold boot — fresh kernel entropy doesn't need a
	// resume hook. On restore, vmmd's TriggerResumeHook will then time out
	// dial-resume and fail closed (per spec §11 V6).
	if err := listenResumeHook(slog.Default()); err != nil {
		slog.Default().Warn("vsock resume listener unavailable", "err", err)
	}

	// Wave 0 PR-C / ADR-047: stateless-runtime advisory. FAN_CLASS_NOTIF
	// on a closed set of state-shaped paths; debounced batches shipped
	// over AF_VSOCK DGRAM (port=1025, msg_type=2) to the host. Returns
	// are tolerated — a guest without CONFIG_FANOTIFY=y still boots,
	// the contract is "no signal" not "won't boot".
	if err := runStatelessAdvisory(slog.Default()); err != nil {
		slog.Default().Warn("stateless advisory unavailable", "err", err)
	}

	// PR #470-FU-B (issue #470): the framework-ready unix-socket
	// proxy. The proxy at /run/guest-init/framework-ready.sock is
	// the runner-facing entry point — runners connect to it with
	// one line ("<runtime> <warmup_ms>") and the proxy frames the
	// vsock DGRAM (port 1027, msg_type 4) payload and forwards to
	// the host. The proxy must start BEFORE the supervisor starts
	// the runners so the first request can't race the proxy coming
	// up. Soft-fail: bind errors log at Warn and the platform
	// contract is "no warm-capture signal" not "won't boot" — the
	// engine's warm-capture wait in PR #470-FU-A times out and
	// falls through to init-tier.
	if err := startFrameworkReadyProxy(slog.Default()); err != nil {
		slog.Default().Warn("framework_ready proxy unavailable", "err", err)
	}

	mode, buildManifest, err := decideMode(os.DirFS("/"))
	if err != nil {
		return err
	}
	if mode == modeBuild {
		return runBuild(buildManifest)
	}

	//nolint:forbidigo // api.AppManifestPath is a compile-time constant defined in pkg/api (/etc/faas/app.json) — the manifest is written into the guest's rootfs by the builder before boot, the customer never writes it. Inside the microVM, the path is not customer-spoofable.
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
	// closure so runAppWithEnv can pull them. A missing or malformed file is
	// not fatal — the app runs without env secrets (consistent with
	// the quota=0 path).
	secrets, secErr := loadSecrets(slog.Default())
	if secErr != nil {
		// Don't surface the value, just the fact that the file failed.
		slog.Default().Warn("secrets.env could not be loaded; proceeding without secrets", "err_kind", errorKind(secErr))
	}

	// Issue #395 / ADR-045: read /etc/faas/env.json (plaintext JSON
	// map, written by vmmd at wake time) and thread it into the
	// 4-layer precedence BuildEnvWithSecrets enforces. A missing or
	// malformed file is not fatal — the app runs without API env
	// (consistent with the secrets layer above). The path is
	// siblings-of secrets, deliberately separate so a JSON-decode
	// failure on one doesn't propagate to the other.
	apiEnv, apiErr := loadAPIEnv(slog.Default())
	if apiErr != nil {
		slog.Default().Warn("env.json could not be loaded; proceeding without api env", "err_kind", errorKind(apiErr))
	}

	// ADR-051 Phase 4: Supervisor holds atomic.Pointer (Run is a
	// pointer receiver, see supervise.go). We assign the struct
	// fields one at a time so the Start closure can refer to
	// `supRef` — referencing `&sup` from inside its own struct
	// literal would trip Go's "undefined: sup" before the
	// assignment. The wiring below is the canonical fix.
	supRef := &Supervisor{Max: MaxRestarts}
	supRef.Start = func() error { return runAppWithEnv(manifest, secrets, apiEnv, supRef) }
	supRef.OnCrash = func(attempt int, err error) {
		fmt.Fprintf(os.Stderr, "guest-init: app crashed (restart %d/%d): %v\n", attempt, MaxRestarts, err)
	}
	// ADR-051 Phase 4: characterize the workload by observing the
	// first cold boot. Runs in parallel with sup.Run() so the
	// PID-fd walk finds the customer's listener without blocking
	// the boot. The host (pkg/fcvm/vmm.go::WaitCharacterization
	// Report in PR-D) gates the RUNNING transition on the report
	// arriving; a timeout here falls back to scan-hint class (never
	// fails the deploy — that's worse than today's opaque "guest
	// not ready after 30s" path).
	go runCharacterizationForSup(supRef, manifest)
	return supRef.Run()
}

// runAppWithEnv is the secrets+apiEnv-aware entrypoint — same execve
// path as runApp but with both layers merged over the manifest env.
// Empty/nil secrets AND empty/nil apiEnv short-circuits to the bare
// BuildEnv path; nil in one of them short-circuits to the other layer's
// 3-arg shape via BuildEnvWithSecrets's nil-tolerant map reads.
func runAppWithEnv(m api.AppManifest, secrets, apiEnv map[string]string, sup *Supervisor) error {
	argv := m.Entrypoint
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.EffectiveWorkingDir()
	env := BuildEnvWithSecrets(os.Environ(), m, secrets, apiEnv)
	// Issue #460 / ADR-053 (PR-C): stamp PORT=<m.EffectivePort()>
	// onto the exec'd env so the runner shim can bind the
	// per-deployment override port. Appended AFTER
	// BuildEnvWithSecrets so a customer-set PORT in manifest env,
	// apiEnv, or sealed secrets cannot accidentally override the
	// platform contract — guest-init is the canonical source of
	// truth for the override port. Unconditional: m.Port == 0
	// still injects PORT=8080, which is harmless for runners that
	// already bind :8080 and matches the vmmd wire-level default.
	// StampOverridePortEnv is the pure helper the unit test pins;
	// keeping the live edit here means the precedence assertion
	// tests the exact code path the production execve uses.
	env = StampOverridePortEnv(env, m.EffectivePort())
	// Issue #555 PR-4: stamp TRACEPARENT onto the runner env. The
	// W3C trace context was shipped from the host via the vsock
	// resume hook; the supervisor reads it via GetResumeTraceparent
	// at Start() time. Empty = no OTel configured, the env is
	// unchanged.
	env = StampTraceparentEnv(env, GetResumeTraceparent())
	cmd.Env = env
	// ADR-051 Phase 4 Slice A PR-B: tee the customer's stdout/stderr
	// into the supervisor's ring buffer so the characterize probe can
	// populate the report's LogTail field. The MultiWriter preserves
	// the live console stream (os.Stdout) so operators watching
	// journalctl -u faas-vmmd still see the boot log in real time.
	// When sup is nil (unit tests that exercise runAppWithEnv directly
	// without a supervisor), we fall back to the legacy bare stdout
	// wiring — those tests don't read LogTail.
	if sup != nil {
		mw := io.MultiWriter(os.Stdout, sup.LogBuffer())
		cmd.Stdout, cmd.Stderr = mw, mw
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	if uid := lookupUID(m.EffectiveUser()); uid > 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)},
		}
	}
	// ADR-051 Phase 4: expose the forked cmd to the supervisor so
	// runCharacterizationForSup can read the PID via LastAppPID().
	// The supervisor's Run() loop captures the cmd at every
	// restart; runAppWithEnv executes once per restart.
	if sup != nil {
		sup.TrackCommand(cmd)
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
	argv := buildArgv(m)

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

// buildArgv constructs the build-engine argv for one BuildManifest. The
// Dockerfile framework uses buildctl; everything else is Railpack with an
// explicit --plan. Extracted from runBuild so the table-driven
// TestBuildArgv can pin the wire shape without spinning up a builder VM.
func buildArgv(m api.BuildManifest) []string {
	switch m.Framework {
	case api.FrameworkDockerfile:
		return []string{
			"buildctl", "build",
			"--frontend", "dockerfile",
			"--local", "context=" + m.Workdir,
			"--local", "dockerfile=" + m.Workdir,
			"--output", "type=oci,dest=" + m.OutDir + "/image.tar",
		}
	}
	// railpack with --plan auto|node|python|go
	plan := "auto"
	switch m.Framework {
	case api.FrameworkRailpackNode:
		plan = "node"
	case api.FrameworkRailpackPython:
		plan = "python"
	case api.FrameworkRailpackGo:
		plan = "go"
	}
	return []string{"railpack", "build", m.OutDir, "--plan", plan}
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

	// Re-attach devtmpfs at the new root's /dev. The earlier mountBasics
	// devtmpfs was on the OLD root and is gone after pivot; the merged
	// overlay's /dev is whatever the base layer shipped (null/console/tty
	// only) — without a fresh devtmpfs the guest has no /dev/hwrng,
	// /dev/urandom, /dev/zero, etc. and the resume hook's reseed step
	// fails on ENOENT.
	if err := syscall.Mount("devtmpfs", "/dev", "devtmpfs", 0, ""); err != nil {
		slog.Default().Warn("post-pivot devtmpfs mount failed", "err", err)
	}
	// Same story for /proc, /sys, /tmp — mountBasics attached them to the
	// OLD root, so they're gone after pivot. Without /proc the resume hook
	// can't read /proc/sys/kernel/random/uuid to record its freshly-rekeyed
	// value (spec §11 V6).
	for _, mnt := range []struct{ src, dst, fs string }{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"tmpfs", "/tmp", "tmpfs"},
	} {
		_ = os.MkdirAll(mnt.dst, 0o755)
		if err := syscall.Mount(mnt.src, mnt.dst, mnt.fs, 0, ""); err != nil {
			slog.Default().Warn("post-pivot mount failed", "dst", mnt.dst, "err", err)
		}
	}
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

// runCharacterizationForSup is the boot-side glue between the
// characterize_linux.go probe and the supervisor. It owns the
// "what PID is the customer app" lifecycle and the "what was the
// last exit code" visibility that runCharacterization needs:
//
//   - `AppPID()` is polled by waitForBind every 50 ms until the
//     supervisor's lastCmd pointer is non-nil. If the supervisor
//     finishes without ever forking (e.g. a test-only stub Start),
//     AppPID returns -1 forever and the probe times out at the
//     bind-dealine → classified `job`.
//   - `WaitForExit()` blocks until the supervisor's Run returns.
//     We bridge by polling the supervisor's lastExitCode via
//     reflection-free access (every 50 ms) and surfacing it as
//     the report's ExitCode.
//
// Returns when runCharacterization returns (duration: ~10s
// deadline or earlier on bind+probe). Errors are warn-logged;
// the platform contract is "no signal" not "won't boot".
func runCharacterizationForSup(sup *Supervisor, manifest api.AppManifest) {
	log := slog.Default()
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	// Exit-code visibility is exposed via WaitForExit below, which
	// polls sup.LastExitCode synchronously. The supervisor's trackExit
	// fires synchronously inside Run() before it returns, so the polling
	// loop unblocks as soon as the app exits — no separate gate goroutine
	// or `done` channel is needed here.

	args := RunArgs{
		Manifest: manifest,
		AppPID:   func() int { return sup.LastAppPID() },
		WaitForExit: func() (int, error) {
			// Block until the supervisor's exit code stabilises.
			for sup.LastExitCode() == -1 {
				time.Sleep(50 * time.Millisecond)
			}
			return sup.LastExitCode(), nil
		},
		RingBufferTail: func() string {
			// Populated from the supervisor's ring buffer (Slice A
			// PR-B). The buffer is allocated lazily on the first
			// LogBuffer() call (cmd.Stdout wiring in runAppWithEnv),
			// so a sup without a forked app returns "" — same
			// shape as the pre-PR-B empty string. The wire-side
			// truncateLog at characterize_linux.go:198 clamps the
			// returned bytes to VsockCharacterizationMaxBody (32
			// KiB), so the 64 KiB buffer's over-budget tail never
			// overflows the JSON body.
			if sup == nil {
				return ""
			}
			return sup.LogTail()
		},
		Log: log,
	}
	runCharacterization(context.Background(), args)
}
