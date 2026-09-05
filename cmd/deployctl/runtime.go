package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/deploycontroller"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

type hostRuntime struct {
	unitDir      string
	databaseURL  string
	serviceOrder []string
	readyTimeout time.Duration
	// waitReady is the readiness probe used by Restart(); defaults
	// to (r).waitReady. PR-B introduced the override seam so the
	// restart-order unit test can stub the probe to nil-true instead
	// of standing up fake sockets / open TCP ports for each daemon
	// (8 services, ~3 probes each = 24 attempts on dev-box ports
	// that are already in use by unrelated processes — flaky).
	// Production sets it to nil (=> default) at construction.
	waitReadyOverride func(ctx context.Context, service string) error
}

const (
	serviceFilePrefix = "faas-"
	serviceFileSuffix = ".service"
)

// legacyManagedServices are the service names that the split-box topology
// retired but that older one-box installs may still leave on disk. They are
// included in topology convergence so a role-specific deployment also
// removes the last mutable legacy service residue.
var legacyManagedServices = []string{"gatewayd", "spool-sync"}

func managedServiceNames() []string {
	names := append([]string(nil), daemonunitspec.ActivationOrder()...)
	return append(names, legacyManagedServices...)
}

// servicesInUnitDir returns the daemons actually shipped by a release
// bundle, in dependency order. daemonunitspec.Registry is the catalog for
// all roles, not the list for one host: a control-plane bundle must not try
// to enable compute-only units that happen to be present in that catalog.
func servicesInUnitDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	services := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, serviceFilePrefix) || !strings.HasSuffix(name, serviceFileSuffix) {
			continue
		}
		service := strings.TrimSuffix(strings.TrimPrefix(name, serviceFilePrefix), serviceFileSuffix)
		if _, err := daemonunitspec.UnitByName(service); err != nil {
			return nil, fmt.Errorf("unknown release daemon unit %q: %w", name, err)
		}
		services[service] = struct{}{}
	}
	if len(services) == 0 {
		return nil, errors.New("release contains no daemon service units")
	}
	return orderedServices(services)
}

// manifestServices extracts the role-specific daemon set from the verified
// bundle manifest. An empty set is retained as a compatibility fallback for
// older unit tests and legacy callers that did not record systemd files.
func manifestServices(manifest releasebundle.Manifest) (map[string]struct{}, bool, error) {
	services := make(map[string]struct{})
	for _, file := range manifest.Files {
		if !strings.HasPrefix(file.Path, "systemd/") {
			continue
		}
		name := strings.TrimPrefix(file.Path, "systemd/")
		if !strings.HasPrefix(name, serviceFilePrefix) || !strings.HasSuffix(name, serviceFileSuffix) {
			continue
		}
		service := strings.TrimSuffix(strings.TrimPrefix(name, serviceFilePrefix), serviceFileSuffix)
		if _, err := daemonunitspec.UnitByName(service); err != nil {
			return nil, false, fmt.Errorf("unknown manifest daemon unit %q: %w", file.Path, err)
		}
		services[service] = struct{}{}
	}
	return services, len(services) > 0, nil
}

func orderedServices(services map[string]struct{}) ([]string, error) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(services))
	for _, service := range order {
		if _, ok := services[service]; ok {
			out = append(out, service)
		}
	}
	if len(out) == len(services) {
		return out, nil
	}
	missing := make([]string, 0, len(services)-len(out))
	seen := make(map[string]struct{}, len(out))
	for _, service := range out {
		seen[service] = struct{}{}
	}
	for service := range services {
		if _, ok := seen[service]; !ok {
			missing = append(missing, service)
		}
	}
	sort.Strings(missing)
	return nil, fmt.Errorf("release daemon units missing from restart order: %s", strings.Join(missing, ", "))
}

func hasService(services []string, wanted string) bool {
	for _, service := range services {
		if service == wanted {
			return true
		}
	}
	return false
}

func (r hostRuntime) restartServices(manifest releasebundle.Manifest) ([]string, error) {
	order := r.serviceOrder
	if len(order) == 0 {
		var err error
		order, err = daemonunitspec.RestartOrder()
		if err != nil {
			return nil, fmt.Errorf("resolve restart order: %w", err)
		}
	}
	services, present, err := manifestServices(manifest)
	if err != nil {
		return nil, err
	}
	if !present {
		return order, nil
	}
	filtered := make([]string, 0, len(services))
	for _, service := range order {
		if _, ok := services[service]; ok {
			filtered = append(filtered, service)
		}
	}
	if len(filtered) != len(services) {
		return nil, fmt.Errorf("manifest daemon units are not covered by restart order")
	}
	return filtered, nil
}

func healthAddressForManifest(manifest releasebundle.Manifest) (string, error) {
	services, present, err := manifestServices(manifest)
	if err != nil {
		return "", err
	}
	if _, ok := services["gatewayd-public"]; ok {
		return "http://127.0.0.1:9092/healthz", nil
	}
	if _, ok := services["gatewayd-internal"]; ok {
		return "http://127.0.0.1:9090/healthz", nil
	}
	if !present {
		return "http://127.0.0.1:9090/healthz", nil
	}
	return "", errors.New("release has no gateway health endpoint")
}

func (r hostRuntime) Preflight(_ context.Context, manifest releasebundle.Manifest, releaseRoot string) error {
	if manifest.Target != "linux/amd64" {
		return fmt.Errorf("unsupported release target %q", manifest.Target)
	}
	services, err := servicesInUnitDir(filepath.Join(releaseRoot, "systemd"))
	if err != nil {
		return fmt.Errorf("required daemon units: %w", err)
	}
	for _, path := range []string{
		filepath.Join(releaseRoot, "bin", "migrate"),
		filepath.Join(releaseRoot, "systemd"),
		"/etc/systemd/system",
		"/run/faas",
		"/dev/shm",
	} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("required path %s: %w", path, err)
		}
	}
	// The base-image staging trees are compute-only resources owned by
	// imaged. A control-plane release must not require the imaged user or
	// the compute disk layout just because the daemon registry contains
	// those daemons for other node roles.
	if hasService(services, "imaged") {
		for _, path := range []string{"/srv/fc/base", "/srv/fc/base-staging", "/srv/fc/scans"} {
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("required compute path %s: %w", path, err)
			}
		}
	}
	return nil
}

func (r hostRuntime) Migrate(ctx context.Context, manifest releasebundle.Manifest, releaseRoot, _ string) error {
	migrate := filepath.Join(releaseRoot, "bin", "migrate")
	if _, err := os.Stat(migrate); err != nil {
		return fmt.Errorf("migration binary: %w", err)
	}
	command := fmt.Sprintf("DATABASE_URL=%q %q", r.databaseURL, migrate)
	return runCommand(ctx, "su", "-", "faas", "-s", "/bin/bash", "-c", command)
}

func (r hostRuntime) Activate(ctx context.Context, releaseRoot string) error {
	units := filepath.Join(releaseRoot, "systemd")
	services, err := servicesInUnitDir(units)
	if err != nil {
		return fmt.Errorf("read release daemon units: %w", err)
	}
	if hasService(services, "imaged") {
		if err := ensureBaseStagingRoots(); err != nil {
			return err
		}
		if err := cleanupAllBaseScratch(); err != nil {
			return fmt.Errorf("cleanup base scratch: %w", err)
		}
	}
	if err := filepath.WalkDir(units, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(units, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return installAtomic(path, filepath.Join(r.unitDir, rel), info.Mode().Perm())
	}); err != nil {
		return fmt.Errorf("install units: %w", err)
	}
	tmpfiles := filepath.Join(releaseRoot, "tmpfiles.d", "faas.conf")
	if info, err := os.Stat(tmpfiles); err == nil && info.Mode().IsRegular() {
		if err := installAtomic(tmpfiles, "/etc/tmpfiles.d/faas.conf", info.Mode().Perm()); err != nil {
			return fmt.Errorf("install tmpfiles rule: %w", err)
		}
		if err := runCommand(ctx, "systemd-tmpfiles", "--create", "/etc/tmpfiles.d/faas.conf"); err != nil {
			return err
		}
	}
	if err := runCommand(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := r.reconcileServiceTopology(ctx, services); err != nil {
		return err
	}
	if err := runCommand(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, service := range services {
		if err := runCommand(ctx, "systemctl", "unmask", "faas-"+service+".service"); err != nil {
			return err
		}
		if err := runCommand(ctx, "systemctl", "enable", "faas-"+service+".service"); err != nil {
			return err
		}
	}
	return nil
}

// reconcileServiceTopology makes the service set in a verified release
// authoritative on the host. Enabling the new role is not sufficient: an
// older one-box or opposite-role install can leave a service enabled and
// inactive, and it will return after reboot. Omitted managed services are
// stopped, disabled, and masked; bundled services are unmasked before they
// are enabled.
func (r hostRuntime) reconcileServiceTopology(ctx context.Context, allowed []string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, service := range allowed {
		allowedSet[service] = struct{}{}
	}
	for _, service := range managedServiceNames() {
		unit := "faas-" + service + ".service"
		if _, ok := allowedSet[service]; ok {
			continue
		}

		unitPath := filepath.Join(r.unitDir, unit)
		masked, err := maskedUnit(unitPath)
		if err != nil {
			return fmt.Errorf("inspect omitted unit %s: %w", unit, err)
		}
		if !masked {
			if _, statErr := os.Lstat(unitPath); statErr == nil {
				if err := runCommand(ctx, "systemctl", "disable", "--now", unit); err != nil {
					return fmt.Errorf("disable omitted unit %s: %w", unit, err)
				}
			} else if !os.IsNotExist(statErr) {
				return fmt.Errorf("inspect omitted unit %s: %w", unit, statErr)
			}
		}
		if !masked {
			// systemd cannot replace a regular unit file in
			// /etc/systemd/system with a mask, even with --force. These
			// files are part of the deployctl-managed FaaS unit namespace,
			// so remove the stale copy after it has been stopped/disabled
			// and before creating the mask. Preserve an existing mask above.
			if _, err := os.Lstat(unitPath); err == nil {
				if err := os.Remove(unitPath); err != nil {
					return fmt.Errorf("remove omitted unit %s: %w", unit, err)
				}
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("inspect omitted unit %s: %w", unit, err)
			}
		}
		if err := runCommand(ctx, "systemctl", "mask", "--force", unit); err != nil {
			return fmt.Errorf("mask omitted unit %s: %w", unit, err)
		}
	}
	return nil
}

// maskedUnit recognises the systemd mask representation without invoking
// systemctl. This lets convergence avoid sending `disable --now` to an
// already-masked unit (which systemd rejects), while still reasserting the
// mask below. Only the unit directory is inspected; these FaaS units are
// installed there by Ansible and the CD path.
func maskedUnit(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return false, err
	}
	return target == "/dev/null", nil
}

// baseStagingRoots are the two controller-owned staging trees the
// daemon unit expects to exist before imaged starts:
//   - /srv/fc/base-staging — disk-backed OCI layer extraction for full
//     base builds (FAAS_BASE_EXTRACT_ROOT). The extracted tree can be
//     gigabytes (a Go toolchain base is ~850 MB unpacked); it must NOT
//     live on the 2 GiB /dev/shm tmpfs (imaged ENOSPC crash-loop,
//     2026-08-05 → 2026-08-06).
//   - /dev/shm/faas-base-staging — tmpfs staging for the parent-ref
//     overlay upper/work dirs (FAAS_BASE_STAGING_ROOT). The kernel
//     rejects overlay mounts whose upper fs doesn't support tmpfile,
//     and host /tmp is ext4, so this one stays on tmpfs (ADR-053).
var baseStagingRoots = []string{
	"/srv/fc/base-staging",
	"/dev/shm/faas-base-staging",
}

// ensureBaseStagingRoots creates both controller-owned staging roots
// with the ownership the faas-imaged unit runs under, so a fresh host
// (or one where the dirs were wiped by a reboot of /dev/shm) starts
// clean. The dirs must be writable by the faas-imaged service user
// (imaged creates faas-base-* temp dirs inside them) — so they are
// chowned to faas-imaged:faas with 0755, not left root-owned. Runs as
// root (deployctl on the host); a failure here is a hard error — the
// unit cannot stage bases without these dirs.
func ensureBaseStagingRoots() error {
	svcUser, err := user.Lookup("faas-imaged")
	if err != nil {
		return fmt.Errorf("lookup faas-imaged: %w", err)
	}
	svcGroup, err := user.LookupGroup("faas")
	if err != nil {
		return fmt.Errorf("lookup faas group: %w", err)
	}
	uid, err := strconv.Atoi(svcUser.Uid)
	if err != nil {
		return fmt.Errorf("parse faas-imaged uid %q: %w", svcUser.Uid, err)
	}
	gid, err := strconv.Atoi(svcGroup.Gid)
	if err != nil {
		return fmt.Errorf("parse faas gid %q: %w", svcGroup.Gid, err)
	}
	for _, root := range baseStagingRoots {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fmt.Errorf("ensure base staging root %s: %w", root, err)
		}
		if err := os.Chown(root, uid, gid); err != nil {
			return fmt.Errorf("chown base staging root %s: %w", root, err)
		}
	}
	return nil
}

// cleanupBaseScratch removes controller-owned stale staging entries
// (faas-base-* extraction dirs and faas-base-mkfs-*.ext4 mkfs temps)
// from the given root, leaving everything else untouched. Only entries
// owned by the faas-imaged service user are removed — a foreign
// directory with a matching name (e.g. an operator's artifact) is
// preserved. The "what is controller-owned" predicate lives in
// pkg/deploycontroller so the dry-run report and this cleanup agree.
func cleanupBaseScratch(root string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	owner, err := user.Lookup("faas-imaged")
	if err != nil {
		// No faas-imaged user on this host — we cannot verify an entry
		// is controller-owned, so remove nothing. Conservative: on a
		// real control-plane host the user exists (bootstrap creates
		// it), and a missing user here means we're not on a host that
		// needs scratch cleanup (e.g. a dev box or a unit test).
		return fmt.Errorf("cleanupBaseScratch: %w", err)
	}
	ownerUID, err := strconv.Atoi(owner.Uid)
	if err != nil {
		return fmt.Errorf("parse faas-imaged uid %q: %w", owner.Uid, err)
	}
	for _, entry := range entries {
		if !deploycontroller.IsControllerStagingEntry(entry) {
			continue
		}
		name := entry.Name()
		full := filepath.Join(root, name)
		info, err := os.Stat(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		// Only remove entries owned by the imaged service user. The
		// syscall.Stat_t fields differ across platforms, but the
		// deployctl runtime only ever runs on the linux host.
		sys, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(sys.Uid) != ownerUID {
			continue
		}
		if entry.IsDir() {
			if err := os.RemoveAll(full); err != nil {
				return err
			}
		} else if err := os.Remove(full); err != nil {
			return err
		}
	}
	return nil
}

// cleanupAllBaseScratch cleans both controller-owned staging roots and
// returns a combined error (best-effort: a failure on one root does not
// skip the other). A missing faas-imaged user is not an error — it just
// means there is nothing controller-owned to clean.
func cleanupAllBaseScratch() error {
	var errs []error
	for _, root := range baseStagingRoots {
		if err := cleanupBaseScratch(root); err != nil {
			var unknownUser *user.UnknownUserError
			if errors.As(err, &unknownUser) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", root, err))
		}
	}
	return errors.Join(errs...)
}

func (r hostRuntime) Restart(ctx context.Context, manifest releasebundle.Manifest) error {
	serviceOrder, err := r.restartServices(manifest)
	if err != nil {
		return err
	}
	for _, service := range serviceOrder {
		if err := runCommand(ctx, "systemctl", "reset-failed", "faas-"+service+".service"); err != nil {
			return err
		}
		if err := r.ensureRunFaasOwnership(ctx); err != nil {
			return err
		}
		if err := runCommand(ctx, "systemctl", "restart", "faas-"+service+".service"); err != nil {
			return err
		}
		if err := r.waitReady(ctx, service); err != nil {
			return err
		}
	}
	return nil
}

// ensureRunFaasOwnership pins /run/faas to root:faas 0775 before each
// service restart. The directory is owned by faas-vmmd's systemd
// RuntimeDirectory=faas, which re-creates it as root:root 0755 on every
// vmmd start (before ExecStartPre runs) and would otherwise leave the
// other daemons (schedd, apid, ...) unable to bind their sockets after
// a restart that recycles vmmd. Mirrors the cd-controlplane pre-restart
// chown (PR-M.2) that closes the same race during the workflow deploy.
func (r hostRuntime) ensureRunFaasOwnership(ctx context.Context) error {
	if err := runCommand(ctx, "chown", "root:faas", "/run/faas"); err != nil {
		return err
	}
	return runCommand(ctx, "chmod", "0775", "/run/faas")
}

func (r hostRuntime) Healthy(ctx context.Context, manifest releasebundle.Manifest) error {
	address, err := healthAddressForManifest(manifest)
	if err != nil {
		return err
	}
	if err := r.waitHTTP(ctx, address); err != nil {
		return err
	}
	return nil
}

func (r hostRuntime) waitReady(ctx context.Context, service string) error {
	if r.waitReadyOverride != nil {
		return r.waitReadyOverride(ctx, service)
	}
	if entry, ok := daemonEntry(service); ok && entry.Lifecycle.ReadyzURL != "" {
		return r.waitHTTP(ctx, entry.Lifecycle.ReadyzURL)
	}
	probe, target, err := readinessProbeForService(service)
	if err != nil {
		return err
	}
	switch probe {
	case daemonunitspec.ProbeUnix:
		return waitPath(ctx, target, r.readyTimeout)
	case daemonunitspec.ProbeTCP:
		return waitTCP(ctx, target, r.readyTimeout)
	case daemonunitspec.ProbeSystemd:
		return waitSystemdActive(ctx, "faas-"+service+".service", r.readyTimeout)
	default:
		return fmt.Errorf("unknown readiness probe for %s", service)
	}
}

func (r hostRuntime) waitHTTP(ctx context.Context, address string) error {
	deadline := time.Now().Add(r.readyTimeout)
	lastStatus := 0
	lastBody := ""
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err == nil {
			response, err := http.DefaultClient.Do(req)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 512))
				_ = response.Body.Close()
				lastStatus = response.StatusCode
				if readErr == nil {
					lastBody = strings.TrimSpace(string(body))
				}
				if response.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		if err := sleepReady(ctx); err != nil {
			return err
		}
	}
	if lastStatus != 0 {
		if lastBody != "" {
			return fmt.Errorf("HTTP check timed out: %s (last status %d: %s)", address, lastStatus, lastBody)
		}
		return fmt.Errorf("HTTP check timed out: %s (last status %d)", address, lastStatus)
	}
	return fmt.Errorf("HTTP check timed out: %s", address)
}

func waitPath(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Mode().Type()&os.ModeSocket != 0 {
			return nil
		}
		if err := sleepReady(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("readiness path timed out: %s", path)
}

func waitTCP(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		if err := sleepReady(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("readiness TCP address timed out: %s", address)
}

func waitSystemdActive(ctx context.Context, service string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := runCommand(ctx, "systemctl", "is-active", "--quiet", service); err == nil {
			return nil
		}
		if err := sleepReady(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("systemd readiness timed out: %s", service)
}

func sleepReady(ctx context.Context) error {
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// runCommand is a package-level variable so tests can stub out
// systemctl invocations without booting a real systemd. Production
// callers see the exec-based default; tests substitute a recorder
// that captures the argv sequence Restart() iterates over so the
// test can assert RestartOrder() actually drives the iteration in
// the documented dependency order.
//
// PR-B made the override path necessary: previously ActivationOrder()
// walked Registry slice order (a flat pass), and there was no
// assertion that the loop emits daemons in the right sequence;
// instead of trying to guess where restart_test.go would live, we
// added the seam and the test together.
//
// Replacing via a package var (not a struct field) preserves the
// call-site readability at runtime.go:Restart — the body still
// reads `runCommand(ctx, ...)` with no `r.` prefix.
var runCommand = func(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func installAtomic(source, destination string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".deploy-unit-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destination)
}

func defaultHostRuntime() hostRuntime {
	// Issue #578 / PR-B: the restart loop walks `RestartOrder()` — the
	// Registry sorted topologically by Lifecycle.After declarations —
	// rather than Registry slice order. The slice order in registry.go
	// is human-readable (vmmd first, imaged last) but operationally
	// wrong: a daemon with an After declaration may sit at a higher
	// index than a daemon it depends on, so a plain slice iteration
	// would restart the dependent before its dependency is up.
	//
	// The two error paths here are typed (ErrUnknownDependency / ErrCycle)
	// so callers can distinguish a registry-editor mistake from a
	// transient sort failure. Today the Registry is hand-curated and
	// can't produce either, so the err return surfaces as a fatal
	// startup error rather than a silent skip.
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		// This is a program-startup error: the binary was built against
		// a Registry that is inconsistent. Fall back to ActivationOrder
		// so the deployctl invocation still does something useful, but
		// log the error so the operator sees it on stderr.
		fmt.Fprintf(os.Stderr, "deployctl: RestartOrder failed (%v); falling back to ActivationOrder\n", err)
		order = daemonunitspec.ActivationOrder()
	}
	return hostRuntime{
		unitDir:      "/etc/systemd/system",
		databaseURL:  "postgres:///faas?host=/run/postgresql&user=faas",
		serviceOrder: order,
		readyTimeout: 60 * time.Second,
	}
}
