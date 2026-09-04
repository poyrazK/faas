//go:build linux

// Workload orchestration (issue #463 / ADR-069 / PR-B).
//
// Each deployment boots zero or more workloads under guest-init:
// one main workload (the customer app) plus 0..N sidecars declared
// in the deployment spec. The boot flow is:
//
//   1. Discover workloads. vmmd stamps /etc/faas/workloads.json on
//      drive1 (the main workload's drive) at wake time. The file
//      is the deployment-level roster: {Main, Sidecars[]} where
//      Main is the main workload's spec and Sidecars is the
//      per-sidecar array (or empty when there are no sidecars).
//
//   2. Run init sidecars sequentially. type=="init" workloads run
//      before main; non-zero exit fails the deploy with
//      failure_class: user_error (AC #1). The supervisor's Max=0
//      policy (no restarts for init — a failed init is a hard fail)
//      is enforced by newSupervisorFor's Max=0 branch.
//
//   3. Run main + long-running sidecars in parallel. type=="main"
//      and type=="sidecar" workloads run concurrently, each under
//      its own Supervisor. A non-essential sidecar crash does NOT
//      fail the deploy; an essential sidecar or main workload crash
//      restarts per the supervisor's Max policy (MaxRestarts).
//
//   4. Characterize the main workload only. The bind-detection
//      probe (characterize_linux.go) reads AppPID() from the MAIN
//      supervisor's *exec.Cmd — a sidecar's TCP listener would
//      mis-classify the boot class (e.g. an init sidecar that
//      binds :8080 would be observed as the main app's listener).
//
// Per-workload secrets/env:
//   - The MAIN workload reads /etc/faas/secrets.env and
//     /etc/faas/env.json from drive1 (the legacy paths). vmmd
//     writes these at wake time via StageSecretsEnv / StageAPIEnv.
//   - Sidecars don't read the main workload's secrets.env / env.json.
//     Image defaults are baked into each sidecar's ext4 at build time;
//     deployment-specific overrides are written to the main workload's
//     instance-scoped upper at wake time under the sidecar's name.
//
// Per-workload cgroups (host side):
//   - vmmd creates nested cgroup scopes under the per-instance
//     scope (writeWorkloadCgroup). These are host-side
//     defense-in-depth scopes.
//
// Per-workload cgroups (in-guest, issue #463 / ADR-069 / PR-B
// AC #4): guest-init mounts cgroup2 at /sys/fs/cgroup (see
// main_linux.go::mountCgroup2, called between pivotInto and
// the supervisor's first workload). runSidecar + runAppWithEnv
// then mkdir a per-workload leaf, write memory.max = spec.
// RamMB << 20, and after exec.Command.Start writes the child
// PID into cgroup.procs. Sidecar OOM is scoped to that leaf
// (cgroup v2 memory controller kills only the offending
// leaf's processes) — the main workload keeps running.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// workloadSpec mirrors the on-disk shape of /etc/faas/workloads.json
// (issue #463 / ADR-069 / PR-B). Must stay in lockstep with
// pkg/fcvm/vmm.go::workloadManifest — vmmd writes the file and
// guest-init reads it; a field rename here requires a parallel
// rename in pkg/fcvm/vmm.go (and the proto wire if either end
// reshapes it).
//
// The build tag here is "linux" because guest-init only runs on
// Linux (the in-guest PID 1 of every microVM). The vmmd
// counterpart compiles on every platform but emits identical
// JSON because the field tags match exactly.
//
// JSON field-order pinning: field declarations stay in
// alphabetical order (mirroring pkg/fcvm.workloadManifest).
// The two structs are a wire pair — a reorder here MUST land
// on the vmmd side in the same commit, and the projected-byte
// budget in pkg/fcvm/vmm.go (projectedWorkloadManifestBytes)
// must be re-derived. See the comment on
// pkg/fcvm/vmm.go::workloadManifest for the rationale and the
// round-trip test that pins the parsed-equivalence contract.
type workloadSpec struct {
	Cmd        []string `json:"cmd,omitempty"`
	Entrypoint []string `json:"entrypoint,omitempty"`
	Essential  bool     `json:"essential"`
	Name       string   `json:"name"`
	Port       int      `json:"port"`
	RamMB      int      `json:"ram_mb"`
	Type       string   `json:"type"` // "main" | "init" | "sidecar"
}

// workloadRosterPath is the deployment-level roster location
// (issue #463 / ADR-069 / PR-B). vmmd writes this file once on
// drive1 at wake time; guest-init reads it after assembleOverlay+
// pivot_root to discover the main workload's spec + the per-
// sidecar array. The same path on every workload's drive
// (because they share overlayfs) means a single read is
// sufficient; the merged-root sees drive1's copy.
//
// WorkloadSpecPath (single-workload envelope at /etc/faas/
// workload.json) is the compatibility per-drive stamp vmmd can
// write for operator visibility (debugging tools can `cat` it
// inside the VM). New sidecars use the immutable, name-scoped
// manifest under /etc/faas/workloads/<name>/workload.json. The
// orchestrator reads the roster and then the matching sidecar
// manifest, not the compatibility stamp.
const workloadRosterPath = "/etc/faas/workloads.json"

// workloadRoster mirrors the deployment-level roster shape.
// Main is the canonical main-workload spec; Sidecars is the
// per-sidecar array (nil/empty = legacy single-workload path).
type workloadRoster struct {
	Main     workloadSpec   `json:"main"`
	Sidecars []workloadSpec `json:"sidecars"`
}

// discoverRoster reads the workload roster from the merged
// root. Returns the parsed roster or an error. A missing file
// is the legacy single-workload path — boot() in main_linux.go
// routes to runAppWithEnv unchanged.
//
// The fs.FS parameter lets the unit test drive discoverRoster
// with testing/fstest.MapFS instead of touching the real root.
// On the live boot path, callers pass os.DirFS("/").
func discoverRoster(fsys fs.FS) (workloadRoster, error) {
	var zero workloadRoster
	data, err := fs.ReadFile(fsys, strings.TrimPrefix(workloadRosterPath, "/"))
	if err != nil {
		return zero, err // caller treats absent as legacy path
	}
	var roster workloadRoster
	if err := json.Unmarshal(data, &roster); err != nil {
		return zero, fmt.Errorf("workload roster: parse %q: %w", workloadRosterPath, err)
	}
	return roster, nil
}

// loadSidecarManifest reads the immutable runtime contract baked into a
// sidecar layer by imaged. The deployment roster carries scheduling policy,
// while this manifest carries the image's effective argv, environment,
// working directory, and user. Validate the name before joining it into the
// overlay path so a malformed roster cannot escape the sidecar directory.
func loadSidecarManifest(name string) (api.AppManifest, error) {
	if !validSidecarWorkloadName(name) {
		return api.AppManifest{}, fmt.Errorf("sidecar workload: invalid name %q", name)
	}
	path := filepath.Join(api.SidecarWorkloadManifestPath, name, "workload.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return api.AppManifest{}, err
	}
	return api.ReadManifest(bytes.NewReader(data))
}

// loadSidecarEnv reads the deployment-specific env overrides staged by vmmd
// into the writable main upper. A missing file means that this sidecar has no
// overrides; any other read or parse failure is fatal so a permissions or
// corruption problem cannot silently drop customer configuration.
func loadSidecarEnv(name string) (map[string]string, error) {
	if !validSidecarWorkloadName(name) {
		return nil, fmt.Errorf("sidecar workload: invalid name %q", name)
	}
	path := filepath.Join(api.SidecarWorkloadManifestPath, name, "env.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	env := make(map[string]string)
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("sidecar workload env: parse %q: %w", path, err)
	}
	return env, nil
}

func validSidecarWorkloadName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || len(name) > 63 {
		return false
	}
	for i, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
		if i == 0 && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// runWorkloads (issue #463 / ADR-069 / PR-B) is the boot-side
// orchestrator. The dispatch order:
//
//  1. Run init sidecars sequentially (each blocking). A non-zero
//     init exit fails the deploy immediately — no main workload
//     starts. (AC #1.)
//  2. Run main + type="sidecar" workloads in parallel, each under
//     its own Supervisor. A main workload crash restarts per the
//     supervisor's Max policy; an essential sidecar crash has the
//     same policy; a non-essential sidecar crash is logged and
//     the other workloads continue. (AC #2 / AC #4.)
//  3. Returns when every supervisor has exited (clean or
//     exhausted its restart budget). The main workload's exit
//     code is the deploy's exit code; non-essential sidecar
//     exits are logged but ignored.
//
// The legacy single-workload path (no roster) is the caller's
// responsibility — boot() in main_linux.go owns that fallback.
// runWorkloads is called ONLY when at least one workload
// roster was discovered.
//
// mainManifest is the legacy api.AppManifest for the main
// workload (passed in from boot's earlier os.Open +
// ReadManifest). The orchestrator uses it for the main
// workload's entrypoint + env. Sidecars' effective command and
// env live in their baked, name-scoped workload manifests;
// guest-init execs those values verbatim. Older sidecar layers
// fall back to /usr/local/bin/start.sh or the roster command.
func runWorkloads(mainManifest api.AppManifest, roster workloadRoster, secrets, apiEnv map[string]string, log *slog.Logger, sidecarProxy *sidecarEventsProxy) error {
	if log == nil {
		log = slog.Default()
	}

	// Defensive cap-2 enforcement (issue #463 / ADR-069 / PR-B
	// review finding #2). The migrations 00118 + 00119 cap the
	// per-deployment sidecar count at 2 server-side; guest-init
	// re-asserts the same limit so a malformed /etc/faas/
	// workloads.json (e.g. one stamped by an older vmmd, or a
	// hand-crafted fixture in a metal test) can't trick the
	// orchestrator into supervising more than 2 sidecars.
	// Matches SidecarCapMax in pkg/api/limits.go — if the cap
	// is ever raised, raise both constants together and update
	// the unit test guest/init/workload_linux_test.go.
	if len(roster.Sidecars) > 2 {
		return fmt.Errorf("workload roster: deployment has %d sidecars; cap is 2 (ADR-069 §Decision 1)", len(roster.Sidecars))
	}

	// Step 1: run init sidecars sequentially (AC #1).
	for i := range roster.Sidecars {
		sc := roster.Sidecars[i]
		if sc.Type != "init" {
			continue
		}
		log.Info("runWorkloads: init sidecar starting",
			"name", sc.Name, "essential", sc.Essential)
		// Issue #463 / ADR-069 / ADR-071 / PR-C §3: stamp the
		// wall-clock start so the sidecar_init_exit envelope
		// (init_ok / init_failed) carries a meaningful
		// duration_ms. The start is captured INSIDE the loop
		// (not above it) so each init sidecar's duration is
		// per-sidecar, not cumulative across the roster.
		startedAt := time.Now()
		sup := newSupervisorFor(sc, secrets, apiEnv, log, sidecarProxy)
		runErr := sup.Run()
		elapsedMs := time.Since(startedAt).Milliseconds()
		// Translate the supervisor's terminal error into the
		// status the audit needs. The supervisor's Run returns
		// nil on a clean exit; a non-nil error wraps the
		// sidecar's exit or restart-budget exhaustion (AC #1's
		// hard fail). We attempt to surface the underlying
		// exec.ExitError code for the audit so operators see
		// the real shell exit rather than the supervisor's
		// "crash-looped after N restart(s)" wrapper. A non
		//-ExitError (e.g. supervisor-internal panic-recovered)
		// falls back to -1 and gets recorded as such.
		exitCode := 0
		status := "init_ok"
		if runErr != nil {
			status = "init_failed"
			var ee *exec.ExitError
			if errors.As(runErr, &ee) {
				exitCode = ee.ExitCode()
			} else {
				exitCode = -1
			}
		}
		// Send the sidecar_init_exit envelope AFTER the
		// supervisor returns, so the audit captures the
		// terminal state. A send error is logged + ignored
		// (the supervisor's terminal state remains the
		// source of truth for "did the deploy succeed"); we
		// never silently fail a deploy because the audit
		// signal didn't make it home.
		if sendErr := sidecarProxy.SendInitExit(sc.Name, status, exitCode, elapsedMs); sendErr != nil {
			log.Warn("runWorkloads: sidecar init_exit send failed",
				"name", sc.Name, "status", status, "err", sendErr)
		}
		if runErr != nil {
			// AC #1: init non-zero exit → user_error.
			log.Error("runWorkloads: init sidecar failed",
				"name", sc.Name, "essential", sc.Essential,
				"exit_code", exitCode, "duration_ms", elapsedMs, "err", runErr)
			return fmt.Errorf("init sidecar %q failed: %w", sc.Name, runErr)
		}
		log.Info("runWorkloads: init sidecar ok",
			"name", sc.Name, "duration_ms", elapsedMs)
	}

	// Step 2: spawn main + type="sidecar" workloads in parallel.
	mainSup := newSupervisorForMain(roster.Main, mainManifest, secrets, apiEnv, log)
	supervisors := []*Supervisor{mainSup}
	for i := range roster.Sidecars {
		sc := roster.Sidecars[i]
		if sc.Type != "sidecar" {
			continue
		}
		supervisors = append(supervisors, newSupervisorFor(sc, secrets, apiEnv, log, sidecarProxy))
	}

	// ADR-051 Phase 4 (Slice A PR-B / issue #463 / ADR-069):
	// characterize the main workload only. A sidecar's TCP listener
	// would mis-classify the boot class (e.g. an init sidecar that
	// binds :8080 would be observed as the main app's listener). The
	// probe (characterize_linux.go) reads AppPID() / WaitForExit /
	// RingBufferTail from the main supervisor; sidecar supervisors
	// carry their own atomic state but the characterize probe never
	// reads them. The probe goroutine races the supervisor goroutines
	// so the bind-walk finds the customer's listener without blocking
	// the boot.
	go runCharacterizationForSup(mainSup, mainManifest)

	// Step 3: run every long-running supervisor in its own
	// goroutine and wait for all of them to exit. A
	// non-essential sidecar crash is contained by its
	// supervisor (Max=0 policy); an essential sidecar or
	// main workload crash triggers the supervisor's restart
	// policy and eventually a non-zero Run() return.
	//
	// Panic-safety (PR-B review finding #4): `defer wg.Done()`
	// is the FIRST defer so it runs even if a later recover()
	// re-panics. A bare recover turns a supervisor-panic into
	// a non-fatal log line so one bad sidecar doesn't take
	// down WaitGroup.Wait() — the rest of the workloads keep
	// running. mainSup is intentionally NOT recovered: a
	// panic in the main supervisor is the deploy's terminal
	// failure and must propagate to the orchestrator's
	// return value.
	var wg sync.WaitGroup
	wg.Add(len(supervisors))
	for i, sup := range supervisors {
		sup := sup
		isMain := i == 0 // supervisors[0] is mainSup (see Step 2)
		go func() {
			defer wg.Done()
			if isMain {
				_ = sup.Run()
				return
			}
			defer func() {
				if r := recover(); r != nil {
					log.Error("runWorkloads: sidecar supervisor panicked",
						"index", i, "recover", fmt.Sprintf("%v", r))
				}
			}()
			_ = sup.Run()
		}()
	}
	wg.Wait()

	// The main workload's exit code is the deploy's exit
	// code; non-essential sidecar exits are logged but
	// ignored. The supervisor's lastErr() surfaces the
	// terminal error from Run().
	if lastErr := mainSup.lastErr(); lastErr != nil {
		return lastErr
	}
	return nil
}

// newSupervisorForMain builds the main workload's supervisor
// (issue #463 / ADR-069 / PR-B). The customer's app spec is
// the legacy api.AppManifest; the workload spec carries the
// per-workload policy (port, ram_mb, essential). The
// supervisor's Start closure runs runAppWithEnv (the legacy
// entrypoint that exec's the manifest's entrypoint with
// the merged env).
func newSupervisorForMain(spec workloadSpec, manifest api.AppManifest, secrets, apiEnv map[string]string, log *slog.Logger) *Supervisor {
	supRef := &Supervisor{Max: MaxRestarts}
	supRef.Start = func() error { return runAppWithRAM(manifest, secrets, apiEnv, supRef, spec.RamMB) }
	supRef.OnCrash = func(attempt int, err error) {
		fmt.Fprintf(os.Stderr, "guest-init: main crashed (restart %d/%d): %v\n", attempt, MaxRestarts, err)
	}
	return supRef
}

// newSupervisorFor builds a sidecar supervisor
// (issue #463 / ADR-069 / PR-B + PR-C §4). New sidecar layers
// carry the image's effective command in a name-scoped manifest;
// older layers fall back to the image's /usr/local/bin/start.sh
// convention or the roster command. The essential flag drives
// the restart policy: non-essential = Max=0 (no restart,
// log-and-continue); essential = Max=MaxRestarts (restart
// per the platform contract). PR-C §4 wires the supervisor's
// OnCrash hook to call SendRestart on the proxy so vmmd
// can increment vmmd_sidecar_restart_total{app, sidecar}.
// A nil sidecarProxy (no-signal contract when bind fails)
// keeps the OnCrash hook log-only.
func newSupervisorFor(spec workloadSpec, secrets, apiEnv map[string]string, log *slog.Logger, sidecarProxy *sidecarEventsProxy) *Supervisor {
	maxRestarts := MaxRestarts
	if !spec.Essential {
		maxRestarts = 0 // non-essential sidecar: log crash, do not restart
	}
	supRef := &Supervisor{Max: maxRestarts}
	supRef.Start = func() error { return runSidecar(spec, secrets, apiEnv, supRef) }
	supRef.OnCrash = func(attempt int, err error) {
		fmt.Fprintf(os.Stderr, "guest-init: sidecar %s crashed (restart %d/%d): %v\n",
			spec.Name, attempt, maxRestarts, err)
		// PR-C §4: ship the sidecar_restart envelope so vmmd
		// can increment <daemon>_sidecar_restart_total AND
		// emit events.SidecarRestart. A send error is
		// best-effort (logged + ignored); the supervisor's
		// restart policy remains the source of truth for
		// "did the sidecar actually come back".
		if sidecarProxy != nil {
			if sErr := sidecarProxy.SendRestart(spec.Name, attempt); sErr != nil {
				log.Warn("sidecar restart emit failed",
					"sidecar", spec.Name, "attempt", attempt, "err", sErr)
			}
		}
	}
	return supRef
}

// runSidecar exec's a sidecar workload (issue #463 /
// ADR-069 / PR-B). New layers provide an immutable manifest
// containing the image's effective command and default environment;
// deployment-specific env overrides are read from the name-scoped
// file staged in the writable main upper. Legacy layers use the
// roster fallback while still accepting the same per-sidecar override.
//
// spec.Name and spec.Port remain the wire-stable scheduling and log fields.
// The effective command and image defaults are baked into the sidecar layer;
// the roster command fields are retained for legacy layers.
func runSidecar(spec workloadSpec, secrets, apiEnv map[string]string, sup *Supervisor) error {
	// New sidecar layers carry an immutable AppManifest under a name-scoped
	// path. It is the only source that can preserve the image's Entrypoint,
	// Cmd, default env, working directory, and user without exposing those values on
	// the wake wire. Older layers fall back to the roster fields so existing
	// snapshots remain bootable during rollout.
	baked, manifestErr := loadSidecarManifest(spec.Name)
	var (
		argv0 string
		argv  []string
		env   []string
		port  int
	)
	if manifestErr == nil {
		if len(baked.Entrypoint) == 0 {
			return fmt.Errorf("run sidecar %s: baked manifest has empty entrypoint", spec.Name)
		}
		argv0 = baked.Entrypoint[0]
		argv = append([]string(nil), baked.Entrypoint[1:]...)
		env = BuildEnv(os.Environ(), baked)
		// The roster is authoritative for the port advertised to the
		// scheduler. Older baked manifests may not carry a port, so use
		// the baked value only as a compatibility fallback.
		port = spec.Port
		if port == 0 {
			port = baked.Port
		}
	} else if isNotExist(manifestErr) {
		argv0, argv = resolveSidecarCommand(spec)
		env = os.Environ()
		port = spec.Port
		// Legacy sidecar layers did not contain a workload manifest. Keep
		// their compatibility path, including the old shared env surface.
		if len(secrets) > 0 || len(apiEnv) > 0 {
			env = BuildEnvWithSecrets(env, api.AppManifest{}, secrets, apiEnv)
		}
	} else {
		return fmt.Errorf("run sidecar %s: load baked manifest: %w", spec.Name, manifestErr)
	}
	// Per-sidecar deployment overrides are staged into the instance-scoped
	// main upper by vmmd. They win over image defaults (and over the legacy
	// shared env fallback), but main-workload secrets/API env never leak into
	// the new sidecar manifest path.
	if sidecarEnv, envErr := loadSidecarEnv(spec.Name); envErr == nil {
		env = BuildEnvWithSecrets(env, api.AppManifest{}, sidecarEnv, nil)
	} else if !isNotExist(envErr) {
		return fmt.Errorf("run sidecar %s: load env overrides: %w", spec.Name, envErr)
	}
	// The scheduler-selected/listen port is authoritative, so stamp it after
	// deployment env overrides rather than allowing a PORT override to change
	// the port advertised to the host bridge.
	if port > 0 {
		env = StampOverridePortEnv(env, port)
	}
	cmd := exec.Command(argv0, argv...)
	cmd.Env = env
	if manifestErr == nil {
		cmd.Dir = baked.EffectiveWorkingDir()
		if uid := lookupUID(baked.EffectiveUser()); uid > 0 {
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)},
			}
		}
	}
	// Issue #463 / ADR-069 / PR-B AC #4: per-workload
	// in-guest cgroup v2 partition. mkdir + write
	// memory.max BEFORE Start so the kernel sees the
	// cap on the very first page fault. The leaf is
	// derived from (type, name) via cgroupSafeName; an
	// invalid safe name or failed write aborts the
	// workload before exec; otherwise it could run
	// without its per-workload cap.
	leaf, cgroupErr := prepareWorkloadCgroup(spec.Type, spec.Name, spec.RamMB, slog.Default())
	if cgroupErr != nil {
		return fmt.Errorf("prepare sidecar workload cgroup %q: %w", spec.Name, cgroupErr)
	}
	// Pipe stdout/stderr into the supervisor's ring buffer
	// (Slice A PR-B contract).
	if sup != nil {
		mw := io.MultiWriter(os.Stdout, sup.LogBuffer())
		cmd.Stdout, cmd.Stderr = mw, mw
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	// ADR-051 Phase 4: expose the forked cmd to the
	// supervisor so runCharacterizationForSup can read the
	// PID via LastAppPID(). The characterize probe filters
	// by workload name, so a sidecar's PID is invisible to
	// the main workload's classify.
	if sup != nil {
		sup.TrackCommand(cmd)
	}
	// Run the sidecar. exec.Command blocks until the sidecar
	// exits; the supervisor's Run() loop captures the exit
	// code via trackExit and decides whether to restart.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("run sidecar %s: %w", spec.Name, err)
	}
	// Issue #463 / ADR-069 / PR-B AC #4: place the
	// forked child into the cgroup leaf so the OOM
	// killer scopes to the leaf (not the workload's
	// siblings). Race window is benign — see
	// placeIntoLeaf's doc.
	if leaf != "" {
		placeIntoLeaf(leaf, cmd.Process.Pid, slog.Default())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("run sidecar %s: %w", spec.Name, err)
	}
	return nil
}

// resolveSidecarCommand (PR-C §6) is the pure-argv derivation
// helper that turns a workloadSpec into the (argv0, argv) tuple
// runSidecar hands to exec.Command. Extracted so the precedence
// rules (Entrypoint > Cmd > baked start.sh) are testable
// without a real fork. The fallback path (/usr/local/bin/start.sh)
// preserves the PR-B contract for images that don't set
// cmd/entrypoint at deploy time.
func resolveSidecarCommand(spec workloadSpec) (argv0 string, argv []string) {
	switch {
	case len(spec.Entrypoint) > 0:
		argv0 = spec.Entrypoint[0]
		argv = append([]string(nil), spec.Entrypoint[1:]...)
		argv = append(argv, spec.Cmd...)
	case len(spec.Cmd) > 0:
		argv0 = spec.Cmd[0]
		argv = append([]string(nil), spec.Cmd[1:]...)
	default:
		argv0 = "/usr/local/bin/start.sh"
		argv = nil
	}
	return argv0, argv
}
