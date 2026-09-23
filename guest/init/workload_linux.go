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
// Host cgroup fence:
//   - vmmd places Firecracker in one aggregate per-instance scope. The host
//     fence caps the VM as a whole; workload-specific limits live in the guest.
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
	"os/signal"
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
	Cmd           []string                    `json:"cmd,omitempty"`
	CPUMillicores int                         `json:"cpu_millicores,omitempty"`
	DiskIOProfile string                      `json:"disk_io_profile,omitempty"`
	DependsOn     []api.WorkloadDependency    `json:"depends_on,omitempty"`
	Entrypoint    []string                    `json:"entrypoint,omitempty"`
	Essential     bool                        `json:"essential"`
	Name          string                      `json:"name"`
	Port          int                         `json:"port"`
	Ports         []api.WorkloadPort          `json:"ports,omitempty"`
	RamMB         int                         `json:"ram_mb"`
	ScratchMB     int                         `json:"scratch_mb,omitempty"`
	StartupProbe  *api.AppManifestHealthcheck `json:"startup_probe,omitempty"`
	Type          string                      `json:"type"` // "main" | "init" | "sidecar"
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

// companionSharedDirectoryRoot is the task-local memory-volume namespace.
// Every workload sees the same per-companion directories at the same absolute
// paths; data is instance-scoped and disappears with the microVM.
const companionSharedDirectoryRoot = "/tmp/gregale/companions"

func companionSharedDirectory(name string) string {
	return filepath.Join(companionSharedDirectoryRoot, name)
}

// workloadRoster mirrors the deployment-level roster shape.
// Main is the canonical main-workload spec; Sidecars is the
// per-sidecar array (nil/empty = legacy single-workload path).
type workloadRoster struct {
	Main     workloadSpec   `json:"main"`
	Sidecars []workloadSpec `json:"sidecars"`
}

type workloadRuntime struct {
	spec  workloadSpec
	sup   *Supervisor
	state *workloadDependencyState
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
	if err := hydrateSidecarPortMetadata(&roster); err != nil {
		return err
	}
	workloadEnv, err := buildWorkloadEndpointEnv(roster, mainManifest)
	if err != nil {
		return err
	}
	deps, err := normalizeWorkloadDependencies(roster)
	if err != nil {
		return err
	}

	runtimes := make(map[string]*workloadRuntime, 1+len(roster.Sidecars))
	mainSup := newSupervisorForMain(roster.Main, mainManifest, secrets, apiEnv, log, workloadEnv)
	runtimes["main"] = &workloadRuntime{spec: roster.Main, sup: mainSup, state: newWorkloadDependencyState()}
	for _, sc := range roster.Sidecars {
		sup := newSupervisorFor(sc, secrets, apiEnv, log, sidecarProxy, workloadEnv)
		if baked, found, manifestErr := sidecarManifestForRuntime(sc.Name); manifestErr != nil {
			return fmt.Errorf("workload %q: load sidecar runtime manifest: %w", sc.Name, manifestErr)
		} else if found {
			// Sidecar image metadata is immutable and stays outside the
			// deployment roster. Project the OCI stop contract onto the
			// supervisor before it can receive a shutdown signal.
			sup.stopSignal = parseStopSignal(baked.StopSignal)
			sup.stopGrace = stopGraceForManifest(baked.StopGracePeriod)
		}
		runtimes[sc.Name] = &workloadRuntime{spec: sc, sup: sup, state: newWorkloadDependencyState()}
	}
	orderedNames, err := workloadStartOrder(roster, deps)
	if err != nil {
		return err
	}
	for _, rt := range runtimes {
		rt := rt
		rt.sup.onStart = func() { close(rt.state.started) }
		rt.sup.onHealthy = func() { close(rt.state.healthy) }
		if rt.spec.Type == "sidecar" && sidecarProxy != nil {
			name := rt.spec.Name
			rt.sup.onHealth = func(status, reason string) {
				if err := sidecarProxy.SendHealth(name, status, reason); err != nil {
					log.Warn("runWorkloads: sidecar health send failed", "name", name, "status", status, "err", err)
				}
			}
		}
	}

	// The characterization probe observes only the main workload's PID.
	go runCharacterizationForSup(mainSup, mainManifest)

	coordCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	var mainErr error
	var stopOnce sync.Once
	stopAll := func(reason string) {
		stopOnce.Do(func() {
			log.Info("runWorkloads: stopping workload set", "reason", reason)
			var stopWg sync.WaitGroup
			for _, rt := range runtimes {
				rt := rt
				stopWg.Add(1)
				go func() {
					defer stopWg.Done()
					sig := rt.sup.stopSignal
					if sig == 0 {
						sig = defaultStopSignal
					}
					grace := stopGraceForManifest(rt.sup.stopGrace)
					if err := rt.sup.Stop(context.Background(), sig, grace); err != nil && !errors.Is(err, os.ErrProcessDone) {
						log.Debug("runWorkloads: graceful stop failed", "name", rt.spec.Name, "signal", sig.String(), "grace", grace.String(), "err", err)
					}
				}()
			}
			stopWg.Wait()
		})
	}

	// runWorkloads is the PID-1 path for multi-workload deployments. Install
	// the same signal bridge as the legacy single-workload path so a shutdown
	// reaches every main/sidecar process, not just the process tracked by the
	// first supervisor.
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh,
		defaultStopSignal,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGHUP,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
		syscall.SIGCHLD,
	)
	signalDone := make(chan struct{})
	go func() {
		defer close(signalDone)
		for {
			select {
			case <-coordCtx.Done():
				return
			case sig := <-sigCh:
				if sig == syscall.SIGCHLD {
					reapOne(log)
					continue
				}
				ss, ok := sig.(syscall.Signal)
				if !ok {
					continue
				}
				configuredStop := false
				for _, rt := range runtimes {
					if rt.sup.stopSignal == ss {
						configuredStop = true
						break
					}
				}
				if ss == defaultStopSignal || configuredStop {
					cancel()
					stopAll("external-signal")
					return
				}
				for _, rt := range runtimes {
					if err := rt.sup.ForwardSignal(ss); err != nil && !errors.Is(err, os.ErrProcessDone) {
						log.Debug("runWorkloads: signal forwarding failed", "name", rt.spec.Name, "signal", ss.String(), "err", err)
					}
				}
				if ss == syscall.SIGINT || ss == syscall.SIGQUIT {
					cancel()
					stopAll("external-signal")
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-signalDone
		signal.Stop(sigCh)
	}()
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
						stopAll("dependency-failure")
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
				if rt.spec.Type == "sidecar" && coordCtx.Err() == nil {
					rt.sup.reportHealth("failed", runErr.Error())
				}
				critical := name == "main" || rt.spec.Essential
				log.Error("runWorkloads: workload exited with error", "name", name, "essential", rt.spec.Essential, "err", runErr)
				if critical {
					resultMu.Lock()
					if mainErr == nil {
						mainErr = runErr
					}
					resultMu.Unlock()
					cancel()
					stopAll("workload-exit")
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

// hydrateSidecarPortMetadata reads the immutable sidecar image manifest before
// endpoint env is built. The roster remains a scheduling contract, while OCI
// ExposedPorts live with the image; loading them here makes multi-port image
// metadata available without widening the wake database or gRPC schema.
func hydrateSidecarPortMetadata(roster *workloadRoster) error {
	if roster == nil {
		return nil
	}
	for i := range roster.Sidecars {
		spec := &roster.Sidecars[i]
		if len(spec.Ports) > 0 || spec.Port != 0 {
			continue
		}
		baked, found, err := sidecarManifestForRuntime(spec.Name)
		if err != nil {
			return fmt.Errorf("workload %q: load sidecar manifest: %w", spec.Name, err)
		}
		if found {
			spec.Ports = append([]api.WorkloadPort(nil), baked.Ports...)
			if len(spec.Ports) == 0 && baked.Port != 0 {
				spec.Ports = []api.WorkloadPort{{Port: baked.Port, Protocol: api.WorkloadPortTCP}}
			}
		}
	}
	return nil
}

// sidecarManifestForRuntime loads the immutable image manifest used to
// configure lifecycle semantics before a sidecar supervisor starts. A legacy
// sidecar layer without a baked manifest is valid and returns found=false.
func sidecarManifestForRuntime(name string) (api.AppManifest, bool, error) {
	return sidecarManifestForRuntimeAt("/", name)
}

func sidecarManifestForRuntimeAt(root, name string) (api.AppManifest, bool, error) {
	var zero api.AppManifest
	directRoot, err := fullRootfsSidecarRootAt(root, name)
	if err != nil {
		return zero, false, fmt.Errorf("resolve sidecar root: %w", err)
	}
	var manifest api.AppManifest
	if directRoot != "" {
		manifest, err = loadSidecarManifestAt(directRoot, name)
	} else {
		manifest, err = loadSidecarManifestAt(root, name)
	}
	if isNotExist(err) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	return manifest, true, nil
}

// newSupervisorForMain builds the main workload's supervisor
// (issue #463 / ADR-069 / PR-B). The customer's app spec is
// the legacy api.AppManifest; the workload spec carries the
// per-workload policy (port, ram_mb, essential). The
// supervisor's Start closure runs runAppWithEnv (the legacy
// entrypoint that exec's the manifest's entrypoint with
// the merged env).
func newSupervisorForMain(spec workloadSpec, manifest api.AppManifest, secrets, apiEnv map[string]string, log *slog.Logger, workloadEnvOpt ...map[string]string) *Supervisor {
	policy, maxRestarts := supervisorPolicyFromManifest(manifest)
	supRef := &Supervisor{
		Max:        maxRestarts,
		Policy:     policy,
		stopSignal: parseStopSignal(manifest.StopSignal),
		stopGrace:  stopGraceForManifest(manifest.StopGracePeriod),
	}
	workloadEnv := firstWorkloadEnv(workloadEnvOpt)
	supRef.Start = func() error {
		return runAppWithRAMAndWorkloadEnv(manifest, secrets, apiEnv, supRef, spec.RamMB, workloadEnv, spec.CPUMillicores)
	}
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
func newSupervisorFor(spec workloadSpec, secrets, apiEnv map[string]string, log *slog.Logger, sidecarProxy *sidecarEventsProxy, workloadEnvOpt ...map[string]string) *Supervisor {
	maxRestarts := MaxRestarts
	if spec.Type == "init" || !spec.Essential {
		maxRestarts = 0 // init and non-essential sidecars do not restart
	}
	supRef := &Supervisor{
		Max:        maxRestarts,
		Policy:     api.RestartPolicyOnFailure,
		stopSignal: defaultStopSignal,
		stopGrace:  MaxAppManifestStopGracePeriodFallback,
	}
	workloadEnv := firstWorkloadEnv(workloadEnvOpt)
	supRef.Start = func() error { return runSidecar(spec, secrets, apiEnv, workloadEnv, supRef) }
	supRef.OnCrash = func(attempt int, err error) {
		fmt.Fprintf(os.Stderr, "guest-init: sidecar %s crashed (restart %d/%d): %v\n",
			spec.Name, attempt, maxRestarts, err)
		supRef.reportHealth("restarting", fmt.Sprintf("restart_%d", attempt))
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
func runSidecar(spec workloadSpec, secrets, apiEnv, workloadEnv map[string]string, sup *Supervisor) error {
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
	// A deployment-level probe override wins over the image's immutable OCI
	// HEALTHCHECK. Both startup gating and ongoing monitoring use this effective
	// manifest, so they cannot drift into different probe definitions.
	effectiveManifest := baked
	if spec.StartupProbe != nil {
		effectiveManifest.Healthcheck = spec.StartupProbe
	}
	healthManifestAvailable := manifestErr == nil || spec.StartupProbe != nil
	// Per-sidecar deployment overrides are staged into the instance-scoped
	// main upper by vmmd. They win over image defaults (and over the legacy
	// shared env fallback), but main-workload secrets/API env never leak into
	// the new sidecar manifest path.
	if sidecarEnv, envErr := loadSidecarEnv(spec.Name); envErr == nil {
		env = BuildEnvWithSecrets(env, api.AppManifest{}, sidecarEnv, nil)
	} else if !isNotExist(envErr) {
		return fmt.Errorf("run sidecar %s: load env overrides: %w", spec.Name, envErr)
	}
	// Sidecars keep their own customer env boundary, but share the platform
	// identity with the main workload for log/error correlation.
	env = StampPlatformIdentityEnv(env, apiEnv)
	// The scheduler-selected/listen port is authoritative, so stamp it after
	// deployment env overrides rather than allowing a PORT override to change
	// the port advertised to the host bridge.
	if port > 0 {
		env = StampOverridePortEnv(env, port)
	}
	env = StampWorkloadIdentityEnv(env)
	env = StampEventPublishEnv(env)
	env = StampRuntimeConfigEnv(env)
	env = stampWorkloadEndpointEnv(env, workloadEnv)
	if directRoot != "" {
		// exec.Command resolves bare names against the guest-init process's
		// host PATH before the child chroots. Resolve them against the image
		// PATH instead, and pass the resulting image-absolute path to execve.
		argv0 = resolveWorkloadCommandPath(directRoot, argv0, env)
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
	leaf, cgroupErr := prepareWorkloadCgroupWithIO(spec.Type, spec.Name, spec.RamMB, slog.Default(), spec.CPUMillicores, spec.DiskIOProfile)
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
		if spec.Type == "sidecar" {
			sup.reportHealth("starting", "process_started")
		}
		if sup.onHealthy != nil && healthManifestAvailable {
			uid := lookupUID(baked.EffectiveUser())
			if directRoot != "" {
				uid = lookupUIDInRoot(directRoot, baked.EffectiveUser())
			}
			if err := runStartupHealthcheck(effectiveManifest, env, cmd.Dir, directRoot, uid, cmd.SysProcAttr, slog.Default()); err != nil {
				if spec.Type == "sidecar" {
					sup.reportHealth("unhealthy", err.Error())
				}
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return fmt.Errorf("run sidecar %s: %w", spec.Name, err)
			}
		}
		sup.markHealthy()
		if spec.Type == "sidecar" {
			sup.reportHealth("healthy", "startup_probe_passed")
		}
	}
	var healthCancel context.CancelFunc
	var healthDone <-chan struct{}
	healthErrCh := make(chan error, 1)
	if healthManifestAvailable && spec.Type == "sidecar" {
		healthCtx, cancelHealth := context.WithCancel(context.Background())
		healthCancel = cancelHealth
		done := make(chan struct{})
		healthDone = done
		go func() {
			defer close(done)
			monitorSidecarHealth(healthCtx, effectiveManifest, env, cmd.Dir, directRoot, lookupUID(effectiveManifest.EffectiveUser()), cmd.SysProcAttr, func(err error) {
				if sup != nil {
					sup.reportHealth("unhealthy", err.Error())
				}
				select {
				case healthErrCh <- err:
				default:
				}
				if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
					slog.Default().Debug("runSidecar: healthcheck kill failed", "name", spec.Name, "err", killErr)
				}
			}, slog.Default())
		}()
	}
	// Issue #463 / ADR-069 / PR-B AC #4: place the
	// forked child into the cgroup leaf so the OOM
	// killer scopes to the leaf (not the workload's
	// siblings). Race window is benign — see
	// placeIntoLeaf's doc.
	if leaf != "" {
		placeIntoLeaf(leaf, cmd.Process.Pid, slog.Default())
	}
	runErr := cmd.Wait()
	if healthCancel != nil {
		healthCancel()
		<-healthDone
	}
	select {
	case healthErr := <-healthErrCh:
		return fmt.Errorf("run sidecar %s: %w", spec.Name, healthErr)
	default:
	}
	if runErr != nil {
		return fmt.Errorf("run sidecar %s: %w", spec.Name, runErr)
	}
	return nil
}

func firstWorkloadEnv(options []map[string]string) map[string]string {
	if len(options) == 0 {
		return nil
	}
	return options[0]
}

// fullRootfsSidecarRoot returns the independently mounted root for a sidecar.
// The historical name remains wire-internal; optimized and full-rootfs main
// images now use the same isolated sidecar mount path.
func fullRootfsSidecarRoot(name string) (string, error) {
	return fullRootfsSidecarRootAt("/", name)
}

const (
	sidecarMountMarkerPath  = "/run/faas/.sidecars-mounted"
	sidecarMountMarkerValue = "gregale-sidecars-mounted-v1\n"
)

func writeSidecarMountMarker(root string) error {
	path := filepath.Join(root, strings.TrimPrefix(sidecarMountMarkerPath, "/"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sidecarMountMarkerValue), 0o400)
}

func sidecarMountMarkerPresent(root string) (bool, error) {
	path := filepath.Join(root, strings.TrimPrefix(sidecarMountMarkerPath, "/"))
	info, err := os.Lstat(path)
	if err != nil {
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("sidecar mount marker is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if string(data) != sidecarMountMarkerValue {
		return false, fmt.Errorf("invalid sidecar mount marker payload")
	}
	return true, nil
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
	mounted, err := sidecarMountMarkerPresent(root)
	if err != nil {
		return "", fmt.Errorf("inspect sidecar mount marker: %w", err)
	}
	if !mounted {
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

// resolveWorkloadCommandPath resolves a bare OCI command name inside an image
// root. exec.Command's normal LookPath runs before Cmd.Env is applied (and,
// for sidecars, before Chroot), so it would consult guest-init's PATH rather
// than the OCI image PATH. Returning an image-absolute candidate also makes a
// missing command fail inside that image instead of selecting a host binary.
func resolveWorkloadCommandPath(root, command string, env []string) string {
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
