//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// databaseUpgradeTimeout bounds pg_upgrade's check and its run.
const databaseUpgradeTimeout = 2 * time.Hour

// upgradeAndStart upgrades the cluster to the booted major, then starts
// it; afterUpgrade finishes the extensions and statistics once it serves.
func (g *databaseGuest) upgradeAndStart() {
	started := time.Now()
	if !g.upgradeCluster() {
		return
	}
	g.log.Info("database upgrade: cluster upgraded", "from", g.upgradeFrom, "to", g.major, "took", time.Since(started).Round(time.Millisecond))
	g.startPostgres()
	go g.afterUpgrade()
}

// upgradeCluster runs pg_upgrade unless a previous boot already did, and
// switches to the new cluster. It fails the guest and reports false when
// the cluster cannot be upgraded.
func (g *databaseGuest) upgradeCluster() bool {
	if !fileExists(databaseUpgradeMarker) {
		if fileExists(filepath.Join(databasePGData, "global", "pg_control.old")) {
			g.fail(dbguest.ErrCodeUpgradeFailed, "a previous upgrade stopped after linking the old cluster's files", nil)
			return false
		}
		if code, err := g.runPgUpgrade(); err != nil {
			g.fail(code, "upgrade the cluster", err)
			return false
		}
		if err := writeSynced(databaseUpgradeAnalyze, nil); err != nil {
			g.fail(dbguest.ErrCodeUpgradeFailed, "record the statistics pass", err)
			return false
		}
		if err := writeSynced(databaseUpgradeMarker, nil); err != nil {
			g.fail(dbguest.ErrCodeUpgradeFailed, "record the upgrade", err)
			return false
		}
	}
	if err := switchUpgradedCluster(); err != nil {
		g.fail(dbguest.ErrCodeUpgradeFailed, "switch to the upgraded cluster", err)
		return false
	}
	return true
}

// runPgUpgrade initializes the new cluster and runs pg_upgrade's check,
// then the upgrade. It returns the error code that fits a failure.
func (g *databaseGuest) runPgUpgrade() (string, error) {
	if err := mountUpgradeBase(); err != nil {
		return dbguest.ErrCodeUpgradeRefused, err
	}
	if err := os.MkdirAll(databaseUpgradeSocketDir, 0o700); err != nil {
		return dbguest.ErrCodeUpgradeRefused, err
	}
	if err := os.Chown(databaseUpgradeSocketDir, g.uid, g.gid); err != nil {
		return dbguest.ErrCodeUpgradeRefused, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseUpgradeTimeout)
	defer cancel()
	control, err := g.upgradeTool(ctx, upgradeOldBinDir(g.upgradeFrom)+"/pg_controldata", databasePGData)
	if err != nil {
		return dbguest.ErrCodeUpgradeRefused, fmt.Errorf("pg_controldata: %w", err)
	}
	checksums, ok := controlDataChecksums(control)
	if !ok {
		return dbguest.ErrCodeUpgradeRefused, errors.New("pg_controldata: no checksum version")
	}
	if g.archive {
		if err := g.archiveOldCluster(ctx, control); err != nil {
			return dbguest.ErrCodeUpgradeRefused, fmt.Errorf("archive the old cluster's WAL: %w", err)
		}
	}
	// A new cluster left by an attempt that never linked starts over.
	if err := os.RemoveAll(databaseUpgradeData); err != nil {
		return dbguest.ErrCodeUpgradeRefused, err
	}
	initdb := upgradeInitdbArgs(g.major, checksums)
	if _, err := g.upgradeTool(ctx, initdb[0], initdb[1:]...); err != nil {
		return dbguest.ErrCodeUpgradeRefused, fmt.Errorf("initdb: %w", err)
	}
	check := pgUpgradeArgs(g.upgradeFrom, g.major, runtime.NumCPU(), true)
	if out, err := g.upgradeTool(ctx, check[0], check[1:]...); err != nil {
		_ = os.RemoveAll(databaseUpgradeData)
		return dbguest.ErrCodeUpgradeRefused, fmt.Errorf("pg_upgrade --check: %w: %s", err, lastLines(out, 20))
	}
	run := pgUpgradeArgs(g.upgradeFrom, g.major, runtime.NumCPU(), false)
	if out, err := g.upgradeTool(ctx, run[0], run[1:]...); err != nil {
		code := dbguest.ErrCodeUpgradeRefused
		if fileExists(filepath.Join(databasePGData, "global", "pg_control.old")) {
			code = dbguest.ErrCodeUpgradeFailed
		} else {
			_ = os.RemoveAll(databaseUpgradeData)
		}
		return code, fmt.Errorf("pg_upgrade: %w: %s", err, lastLines(out, 20))
	}
	return "", nil
}

// archiveOldCluster puts all the old cluster's WAL in the archive: a
// cluster that did not shut down cleanly is recovered on its own binaries
// first, with archiving on, and whatever its archiver left waiting is
// archived.
func (g *databaseGuest) archiveOldCluster(ctx context.Context, control string) error {
	if state, _ := controlDataField(control, "Database cluster state"); state != databaseCleanShutdown {
		g.log.Info("database upgrade: recovering the old cluster first", "state", state)
		if err := g.recoverOldCluster(ctx); err != nil {
			return err
		}
	}
	return archivePendingWAL()
}

// recoverOldCluster runs the old major's postgres on the cluster until it
// accepts connections, its recovery done, then shuts it down cleanly: the
// shutdown archives its last WAL file.
func (g *databaseGuest) recoverOldCluster(ctx context.Context) error {
	if err := g.writeHBA(); err != nil {
		return err
	}
	args := recoveryServerArgs(g.upgradeFrom, g.memKiB)
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// The crash left the old postmaster.pid: only this postmaster's counts.
	pid := strconv.Itoa(cmd.Process.Pid)
	for ready := false; !ready; {
		select {
		case err := <-exited:
			return fmt.Errorf("postgres %d exited recovering the cluster: %w", g.upgradeFrom, err)
		case <-time.After(100 * time.Millisecond):
		}
		data, err := os.ReadFile(filepath.Join(databasePGData, "postmaster.pid"))
		ready = err == nil && strings.HasPrefix(string(data), pid+"\n") && postmasterStatus(data) == "ready"
	}
	if err := cmd.Process.Signal(unix.SIGINT); err != nil {
		return err
	}
	if err := <-exited; err != nil {
		return fmt.Errorf("postgres %d shut down: %w", g.upgradeFrom, err)
	}
	return nil
}

// archivePendingWAL archives the WAL files the old cluster's archiver
// left waiting, marking each done as the archiver would.
func archivePendingWAL() error {
	status := filepath.Join(databasePGData, "pg_wal", "archive_status")
	entries, err := os.ReadDir(status)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, name := range pendingWAL(names) {
		if err := archiveWALFile(filepath.Join(databasePGData, "pg_wal", name), name); err != nil {
			return fmt.Errorf("archive %s: %w", name, err)
		}
		if err := os.Rename(filepath.Join(status, name+".ready"), filepath.Join(status, name+".done")); err != nil {
			return err
		}
	}
	return nil
}

// switchUpgradedCluster makes the upgraded cluster the volume's cluster
// and drops the old one, whose files the new one shares; each step is
// safe to repeat after a crash.
func switchUpgradedCluster() error {
	if fileExists(databaseUpgradeData) {
		if fileExists(databasePGData) {
			if err := os.Rename(databasePGData, databaseUpgradeRetired); err != nil {
				return err
			}
		}
		if err := os.Rename(databaseUpgradeData, databasePGData); err != nil {
			return err
		}
		if err := syncDir(databaseMountPoint); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(databaseUpgradeRetired); err != nil {
		return err
	}
	if err := os.Remove(databaseUpgradeMarker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(databaseMountPoint)
}

// afterUpgrade runs, once the upgraded cluster serves, the extension
// updates pg_upgrade asked for and a statistics pass: pg_upgrade carries
// no planner statistics over. A boot that finds the pass unfinished runs
// it again.
func (g *databaseGuest) afterUpgrade() {
	deadline := time.Now().Add(10 * time.Minute)
	for g.currentState() != dbguest.StateReady {
		if g.currentState() == dbguest.StateFailed || time.Now().After(deadline) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseUpgradeTimeout)
	defer cancel()
	if fileExists(databaseUpgradeExtensions) {
		args := append(databasePsqlArgs(g.major)[:len(databasePsqlArgs(g.major))-2], "-f", databaseUpgradeExtensions)
		if out, err := g.upgradeAdmin(ctx, args...); err != nil {
			g.log.Warn("database upgrade: update extensions", "err", err, "output", lastLines(out, 10))
		} else {
			_ = os.Remove(databaseUpgradeExtensions)
		}
	}
	vacuum := []string{databaseBinDir(g.major) + "/vacuumdb", "--all", "--analyze-in-stages", "-h", databaseSocketDir, "-U", databaseOSUser}
	if out, err := g.upgradeAdmin(ctx, vacuum...); err != nil {
		g.log.Warn("database upgrade: analyze", "err", err, "output", lastLines(out, 10))
		return
	}
	if err := os.Remove(databaseUpgradeAnalyze); err != nil && !errors.Is(err, os.ErrNotExist) {
		g.log.Warn("database upgrade: clear the statistics mark", "err", err)
	}
	g.log.Info("database upgrade: statistics rebuilt")
}

func mountUpgradeBase() error {
	if err := os.MkdirAll(databaseUpgradeOldBase, 0o755); err != nil {
		return err
	}
	if fileExists(databaseUpgradeOldBase + "/usr/lib/postgresql") {
		return nil // mounted by an earlier attempt of this boot
	}
	if err := unix.Mount(databaseUpgradeBaseDevice, databaseUpgradeOldBase, "ext4", unix.MS_RDONLY|unix.MS_NODEV|unix.MS_NOSUID, ""); err != nil {
		return fmt.Errorf("mount the old major's base: %w", err)
	}
	return nil
}

// upgradeTool runs one tool as the database user from the volume's root,
// where pg_upgrade writes its scripts, and returns its combined output.
func (g *databaseGuest) upgradeTool(ctx context.Context, name string, args ...string) (string, error) {
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, name, args...))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limitedWriter{w: &out, n: 64 << 10}, &limitedWriter{w: &out, n: 64 << 10}
	err := cmd.Run()
	return out.String(), err
}

// upgradeAdmin is upgradeTool for the guest's superuser sessions.
func (g *databaseGuest) upgradeAdmin(ctx context.Context, args ...string) (string, error) {
	cmd := g.asSuperuser(exec.CommandContext(ctx, args[0], args[1:]...))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limitedWriter{w: &out, n: 64 << 10}, &limitedWriter{w: &out, n: 64 << 10}
	err := cmd.Run()
	return out.String(), err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeSynced(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// syncDir makes renames in dir durable. dir is one of guest-init's own
// fixed directories on the volume, never a customer path.
func syncDir(dir string) error {
	f, err := os.Open(dir) //nolint:forbidigo // a fixed guest-init directory, opened only to fsync it
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// lastLines keeps a tool's last n lines for the console.
func lastLines(out string, n int) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
