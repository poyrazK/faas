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
//   2. Resolve the dependency graph and start each workload once its
//      dependencies reach started, healthy, or completed_successfully.
//      Init workloads are implicit completed_successfully prerequisites
//      of main and long-running sidecars, preserving the original order.
//
//   3. Each workload runs under its own Supervisor. A non-essential
//      sidecar crash does NOT fail the deploy; an essential sidecar or
//      main workload crash stops the workload set after its restart policy
//      is exhausted.
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
	"context"
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
	Cmd        []string                 `json:"cmd,omitempty"`
	DependsOn  []api.WorkloadDependency `json:"depends_on,omitempty"`
	Entrypoint []string                 `json:"entrypoint,omitempty"`
	Essential  bool                     `json:"essential"`
	Name       string                   `json:"name"`
	Port       int                      `json:"port"`
	RamMB      int                      `json:"ram_mb"`
	Type       string                   `json:"type"` // "main" | "init" | "sidecar"
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
	return loadSidecarManifestAt("/", name)
}

// loadSidecarManifestAt reads a sidecar's immutable image contract from the
// supplied root. The normal overlay path uses "/"; a full-rootfs deployment
// keeps the sidecar artifact outside the main pivot root, so runSidecar reads
// the same contract from that sidecar's own mounted /upper tree.
func loadSidecarManifestAt(root, name string) (api.AppManifest, error) {
	if !validSidecarWorkloadName(name) {
		return api.AppManifest{}, fmt.Errorf("sidecar workload: invalid name %q", name)
	}
	if root == "" {
		root = "/"
	}
	path, err := safeRootPath(root, filepath.Join(strings.TrimPrefix(api.SidecarWorkloadManifestPath, "/"), name, "workload.json"))
	if err != nil {
		return api.AppManifest{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return api.AppManifest{}, err
	}
	return api.ReadManifest(bytes.NewReader(data))
}

// safeRootPath resolves an image-relative path while proving the result stays
// beneath root. Direct-root sidecars are inspected before the child chroots;
// following an image-provided absolute symlink with os.ReadFile directly would
// otherwise resolve against the guest-init process root.
func safeRootPath(root, rel string) (string, error) {
	if root == "" {
		root = "/"
	}
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("root path must be relative")
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("root path escapes image root")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relResolved, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || relResolved == ".." || strings.HasPrefix(relResolved, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("root path escapes image root")
	}
	return resolved, nil
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
	if len(roster.Sidecars) > 2 {
		return fmt.Errorf("workload roster: deployment has %d sidecars; cap is 2 (ADR-069 §Decision 1)", len(roster.Sidecars))
	}
	deps, err := normalizeWorkloadDependencies(roster)
	if err != nil {
		return err
	}

	type workloadRuntime struct {
		spec  workloadSpec
		sup   *Supervisor
		state *workloadDependencyState
	}
	runtimes := make(map[string]*workloadRuntime, 1+len(roster.Sidecars))
	mainSup := newSupervisorForMain(roster.Main, mainManifest, secrets, apiEnv, log)
	runtimes["main"] = &workloadRuntime{spec: roster.Main, sup: mainSup, state: newWorkloadDependencyState()}
	for _, sc := range roster.Sidecars {
		runtimes[sc.Name] = &workloadRuntime{spec: sc, sup: newSupervisorFor(sc, secrets, apiEnv, log, sidecarProxy), state: newWorkloadDependencyState()}
	}
	orderedNames, err := workloadStartOrder(roster, deps)
	if err != nil {
		return err
	}
	for _, rt := range runtimes {
		rt := rt
		rt.sup.onStart = func() { close(rt.state.started) }
		rt.sup.onHealthy = func() { close(rt.state.healthy) }
	}

	// The characterization probe observes only the main workload's PID.
	go runCharacterizationForSup(mainSup, mainManifest)

	coordCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	var mainErr error
	stopAll := func() {
		for _, rt := range runtimes {
			rt.sup.RequestStop()
			if err := rt.sup.ForwardSignal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
				log.Debug("runWorkloads: stop signal forwarding failed", "name", rt.spec.Name, "err", err)
			}
		}
	}
	for _, name := range orderedNames {
		rt := runtimes[name]
		name, rt := name, rt
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, dep := range deps[name] {
				if depErr := waitForWorkloadDependency(coordCtx, dep, runtimes[dep.Name].state); depErr != nil {
					rt.state.setResult(fmt.Errorf("workload %q cannot start: %w", name, depErr))
					if name == "main" || rt.spec.Essential {
						resultMu.Lock()
						if mainErr == nil {
							mainErr = rt.state.result()
						}
						resultMu.Unlock()
						cancel()
						stopAll()
					}
					return
				}
			}
			select {
			case <-coordCtx.Done():
				rt.state.setResult(coordCtx.Err())
				return
			default:
			}
			startedAt := time.Now()
			log.Info("runWorkloads: workload starting", "name", name, "type", rt.spec.Type, "essential", rt.spec.Essential)
			var runErr error
			if name == "main" {
				runErr = rt.sup.Run()
			} else {
				func() {
					defer func() {
						if r := recover(); r != nil {
							runErr = fmt.Errorf("workload supervisor panicked: %v", r)
							log.Error("runWorkloads: sidecar supervisor panicked", "name", name, "recover", fmt.Sprintf("%v", r))
						}
					}()
					runErr = rt.sup.Run()
				}()
			}
			rt.state.setResult(runErr)
			if rt.spec.Type == "init" {
				status, exitCode := "init_ok", 0
				if runErr != nil {
					status, exitCode = "init_failed", -1
					var ee *exec.ExitError
					if errors.As(runErr, &ee) {
						exitCode = ee.ExitCode()
					}
				}
				if sidecarProxy != nil {
					if sendErr := sidecarProxy.SendInitExit(name, status, exitCode, time.Since(startedAt).Milliseconds()); sendErr != nil {
						log.Warn("runWorkloads: sidecar init_exit send failed", "name", name, "status", status, "err", sendErr)
					}
				}
			}
			if runErr != nil {
				critical := name == "main" || rt.spec.Essential
				log.Error("runWorkloads: workload exited with error", "name", name, "essential", rt.spec.Essential, "err", runErr)
				if critical {
					resultMu.Lock()
					if mainErr == nil {
						mainErr = runErr
					}
					resultMu.Unlock()
					cancel()
					stopAll()
				}
			}
		}()
	}
	wg.Wait()
	if mainErr != nil {
		return mainErr
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
	policy, maxRestarts := supervisorPolicyFromManifest(manifest)
	supRef := &Supervisor{Max: maxRestarts, Policy: policy}
	supRef.Start = func() error { return runAppWithRAM(manifest, secrets, apiEnv, supRef, spec.RamMB) }
	supRef.OnCrash = func(attempt int, err error) {
		fmt.Fprintf(os.Stderr, "guest-init: main restart (restart %d/%d policy=%s): %v\n", attempt, maxRestarts, policy, err)
	}
	return supRef
}

// newSupervisorFor builds a sidecar supervisor
// (issue #463 / ADR-069 / PR-B + PR-C §4). New sidecar layers
// carry the image's effective command in a name-scoped manifest;
// older layers fall back to the image's /usr/local/bin/start.sh
// convention or the roster command. Init workloads run once;
// non-essential sidecars use Max=0 (log-and-continue), and
// essential long-running sidecars use Max=MaxRestarts (restart
// per the platform contract). PR-C §4 wires the supervisor's
// OnCrash hook to call SendRestart on the proxy so vmmd
// can increment vmmd_sidecar_restart_total{app, sidecar}.
// A nil sidecarProxy (no-signal contract when bind fails)
// keeps the OnCrash hook log-only.
func newSupervisorFor(spec workloadSpec, secrets, apiEnv map[string]string, log *slog.Logger, sidecarProxy *sidecarEventsProxy) *Supervisor {
	maxRestarts := MaxRestarts
	if spec.Type == "init" || !spec.Essential {
		maxRestarts = 0 // init and non-essential sidecars do not restart
	}
	supRef := &Supervisor{Max: maxRestarts, Policy: api.RestartPolicyOnFailure}
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
	directRoot, rootErr := fullRootfsSidecarRoot(spec.Name)
	if rootErr != nil {
		return fmt.Errorf("run sidecar %s: resolve direct root: %w", spec.Name, rootErr)
	}
	// New sidecar layers carry an immutable AppManifest under a name-scoped
	// path. It is the only source that can preserve the image's Entrypoint,
	// Cmd, default env, working directory, and user without exposing those values on
	// the wake wire. Older layers fall back to the roster fields so existing
	// snapshots remain bootable during rollout.
	var baked api.AppManifest
	var manifestErr error
	if directRoot != "" {
		baked, manifestErr = loadSidecarManifestAt(directRoot, spec.Name)
	} else {
		baked, manifestErr = loadSidecarManifest(spec.Name)
	}
	if directRoot != "" && isNotExist(manifestErr) {
		return fmt.Errorf("run sidecar %s: direct-root image is missing workload manifest", spec.Name)
	}
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
	if directRoot != "" {
		// exec.Command resolves bare names against the guest-init process's
		// host PATH before the child chroots. Resolve them against the image
		// PATH instead, and pass the resulting image-absolute path to execve.
		argv0 = resolveSidecarCommandPath(directRoot, argv0, env)
	}
	cmd := exec.Command(argv0, argv...)
	cmd.Env = env
	var procAttr syscall.SysProcAttr
	if directRoot != "" {
		// A full-rootfs sidecar is a real OCI root, not a lower layer in
		// the main overlay. Give it a private mount namespace before
		// chrooting so a root user inside the image cannot alter mounts
		// visible to the main workload or its sibling sidecars.
		procAttr.Unshareflags = syscall.CLONE_NEWNS
		procAttr.Chroot = directRoot
	}
	if manifestErr == nil {
		cmd.Dir = baked.EffectiveWorkingDir()
		uid := lookupUID(baked.EffectiveUser())
		if directRoot != "" {
			uid = lookupUIDInRoot(directRoot, baked.EffectiveUser())
		}
		if uid > 0 {
			procAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)}
		}
	}
	if directRoot != "" || procAttr.Credential != nil {
		cmd.SysProcAttr = &procAttr
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
	if sup != nil {
		sup.markStarted()
		if sup.onHealthy != nil && manifestErr == nil {
			uid := lookupUID(baked.EffectiveUser())
			if directRoot != "" {
				uid = lookupUIDInRoot(directRoot, baked.EffectiveUser())
			}
			if err := runStartupHealthcheck(baked, env, cmd.Dir, directRoot, uid, cmd.SysProcAttr, slog.Default()); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return fmt.Errorf("run sidecar %s: %w", spec.Name, err)
			}
		}
		sup.markHealthy()
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

// fullRootfsSidecarRoot returns the mounted root for a sidecar when the main
// guest boot selected the full-rootfs path. An absent mount is the optimized
// shared-base path and returns an empty string so existing overlay behavior is
// unchanged.
func fullRootfsSidecarRoot(name string) (string, error) {
	return fullRootfsSidecarRootAt("/", name)
}

func fullRootfsMarkerPresent(root string) (bool, error) {
	if root == "" {
		root = "/"
	}
	marker := filepath.Join(root, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
	info, err := os.Lstat(marker)
	if err != nil {
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("full-rootfs marker is not a regular file")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return false, err
	}
	if string(data) != api.FullRootfsMarkerValue {
		return false, fmt.Errorf("invalid full-rootfs marker payload")
	}
	return true, nil
}

func fullRootfsSidecarRootAt(root, name string) (string, error) {
	if !validSidecarWorkloadName(name) {
		return "", fmt.Errorf("invalid workload name %q", name)
	}
	if root == "" {
		root = "/"
	}
	fullRootfs, err := fullRootfsMarkerPresent(root)
	if err != nil {
		return "", fmt.Errorf("inspect full-rootfs marker: %w", err)
	}
	if !fullRootfs {
		return "", nil
	}
	path := filepath.Join(root, strings.TrimPrefix(api.FullRootfsSidecarMountPath, "/"), name, "upper")
	info, err := os.Lstat(path)
	if err != nil {
		if isNotExist(err) {
			return "", fmt.Errorf("full-rootfs sidecar root %s is missing", path)
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("mounted root %s is a symlink", path)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("mounted root %s is not a directory", path)
	}
	return path, nil
}

const defaultSidecarPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// resolveSidecarCommandPath resolves a bare OCI command name inside a direct
// sidecar root. exec.Command's normal LookPath runs before Chroot and would
// therefore consult the guest-init binary's PATH. Returning an image-absolute
// candidate also makes a missing command fail inside the chroot rather than
// accidentally selecting a host executable.
func resolveSidecarCommandPath(root, command string, env []string) string {
	if root == "" || strings.Contains(command, "/") {
		return command
	}
	pathValue := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathValue = strings.TrimPrefix(kv, "PATH=")
			break
		}
	}
	if pathValue == "" {
		pathValue = defaultSidecarPath
	}
	var firstCandidate string
	for _, dir := range strings.Split(pathValue, ":") {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		candidate := filepath.Join(dir, command)
		if firstCandidate == "" {
			firstCandidate = candidate
		}
		imagePath := filepath.Join(root, strings.TrimLeft(dir, "/"), command)
		info, err := os.Stat(imagePath)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	if firstCandidate != "" {
		return firstCandidate
	}
	return "/" + command
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
