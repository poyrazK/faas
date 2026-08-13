package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
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
}

func (r hostRuntime) Preflight(_ context.Context, manifest releasebundle.Manifest, releaseRoot string) error {
	if manifest.Target != "linux/amd64" {
		return fmt.Errorf("unsupported release target %q", manifest.Target)
	}
	if err := ensureBaseStagingRoots(); err != nil {
		return err
	}
	for _, path := range []string{
		filepath.Join(releaseRoot, "bin", "migrate"),
		filepath.Join(releaseRoot, "systemd"),
		filepath.Join(releaseRoot, "observability"),
		"/etc/systemd/system",
		"/run/faas",
		"/srv/fc/base",
		"/srv/fc/base-staging",
		"/srv/fc/scans",
		"/dev/shm",
	} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("required path %s: %w", path, err)
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
	if err := ensureBaseStagingRoots(); err != nil {
		return err
	}
	if err := cleanupAllBaseScratch(); err != nil {
		return fmt.Errorf("cleanup base scratch: %w", err)
	}
	units := filepath.Join(releaseRoot, "systemd")
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
	if err := r.activateObservability(ctx, releaseRoot); err != nil {
		return err
	}
	if err := runCommand(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, service := range daemonunitspec.ActivationOrder() {
		if err := runCommand(ctx, "systemctl", "enable", "faas-"+service+".service"); err != nil {
			return err
		}
	}
	return nil
}

// activateObservability installs the release bundle's static
// observability files (prometheus.yml, alert rules, prometheus +
// alertmanager units) and reloads the running Prometheus so the
// dashboard pipeline always matches the deployed daemon set.
//
// The bundle ships a fully static config (deploy/controlplane/
// observability/prometheus.yml) so no host-side templating is needed;
// the manifest's checksums verify it before activation.
func (r hostRuntime) activateObservability(ctx context.Context, releaseRoot string) error {
	obs := filepath.Join(releaseRoot, "observability")
	if _, err := os.Stat(obs); err != nil {
		if os.IsNotExist(err) {
			return nil // no observability assets in this release
		}
		return err
	}
	mode := fs.FileMode(0o644)
	for _, file := range []string{"prometheus.yml", "faas.rules.yml", "pg_backup.rules.yml"} {
		src := filepath.Join(obs, file)
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("observability asset %s: %w", file, err)
		}
		if err := installAtomic(src, filepath.Join("/etc/prometheus", file), mode); err != nil {
			return fmt.Errorf("install %s: %w", file, err)
		}
	}
	for _, unit := range []string{"prometheus.service", "alertmanager.service"} {
		src := filepath.Join(obs, unit)
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("observability unit %s: %w", unit, err)
		}
		if err := installAtomic(src, filepath.Join(r.unitDir, unit), mode); err != nil {
			return fmt.Errorf("install %s: %w", unit, err)
		}
	}
	// Validate the rendered config before reloading. promtool lives at
	// /usr/local/bin/promtool on the box (ansible prometheus role);
	// if it's missing, skip validation but still reload.
	if err := runCommand(ctx, "/usr/local/bin/promtool", "check", "config", "/etc/prometheus/prometheus.yml"); err != nil {
		return fmt.Errorf("promtool check config: %w", err)
	}
	_ = runCommand(ctx, "systemctl", "daemon-reload")
	_ = runCommand(ctx, "systemctl", "enable", "prometheus.service")
	_ = runCommand(ctx, "systemctl", "enable", "alertmanager.service")
	// Reload (not restart) so active series/wal survive; first deploy
	// falls back to start via reload-or-restart.
	if err := runCommand(ctx, "systemctl", "reload-or-restart", "prometheus.service"); err != nil {
		return fmt.Errorf("reload prometheus: %w", err)
	}
	return nil
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

func (r hostRuntime) Restart(ctx context.Context, _ releasebundle.Manifest) error {
	for _, service := range r.serviceOrder {
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

func (r hostRuntime) Healthy(ctx context.Context, _ releasebundle.Manifest) error {
	if err := r.waitHTTP(ctx, "http://127.0.0.1:9090/healthz"); err != nil {
		return err
	}
	return nil
}

func (r hostRuntime) waitReady(ctx context.Context, service string) error {
	for _, entry := range daemonunitspec.Registry {
		if entry.Name != service {
			continue
		}
		switch entry.Lifecycle.Probe {
		case daemonunitspec.ProbeUnix:
			return waitPath(ctx, entry.Lifecycle.ProbeTarget, r.readyTimeout)
		case daemonunitspec.ProbeTCP:
			return waitTCP(ctx, entry.Lifecycle.ProbeTarget, r.readyTimeout)
		case daemonunitspec.ProbeSystemd:
			return waitSystemdActive(ctx, "faas-"+service+".service", r.readyTimeout)
		default:
			return fmt.Errorf("unknown readiness probe for %s", service)
		}
	}
	return fmt.Errorf("unknown service %q", service)
}

func (r hostRuntime) waitHTTP(ctx context.Context, address string) error {
	deadline := time.Now().Add(r.readyTimeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err == nil {
			response, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		if err := sleepReady(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("health check timed out: %s", address)
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

func runCommand(ctx context.Context, name string, args ...string) error {
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
	return hostRuntime{
		unitDir:      "/etc/systemd/system",
		databaseURL:  "postgres:///faas?host=/run/postgresql&user=faas",
		serviceOrder: daemonunitspec.ActivationOrder(),
		readyTimeout: 60 * time.Second,
	}
}
