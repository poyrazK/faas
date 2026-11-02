package main

import (
	"bufio"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

// In-place major upgrades (ADR-1027). The VM boots the new major's base
// with the volume of an older cluster and that older major's base as a
// third, read-only drive, whose binaries pg_upgrade runs. pg_upgrade
// --link hard-links the cluster's files into a new cluster beside it, so
// the upgrade takes seconds and little space whatever the database's size.
//
// Its one irreversible moment is when pg_upgrade renames the old
// cluster's global/pg_control to pg_control.old and starts linking: from
// then on the old cluster cannot start. So:
//   - before that (pg_upgrade's check, or a failure before linking), the
//     old cluster is intact: the upgrade is refused and the database
//     boots its old major again;
//   - pg_control.old without the done marker is an upgrade that failed
//     or was interrupted after linking: the database needs a restore;
//   - the done marker, written after pg_upgrade succeeded, finishes the
//     switch to the new cluster on any later boot, idempotently.
//
// The new cluster starts on a new pg_wal, so before pg_upgrade the old
// cluster's WAL must all be in the archive: a point-in-time restore to
// before the upgrade replays it on the old major. A cluster that did not
// shut down cleanly is first recovered on its old binaries with archiving
// on (pg_upgrade would recover it with archiving off), and any WAL file
// still waiting for the archiver is archived; failing that refuses the
// upgrade.
const (
	// databaseUpgradeBaseDevice is the old major's base: the VM's third
	// drive.
	databaseUpgradeBaseDevice = "/dev/vdc"
	databaseUpgradeRoot       = "/run/gregale-upgrade"
	databaseUpgradeOldBase    = databaseUpgradeRoot + "/old"
	databaseUpgradeSocketDir  = databaseUpgradeRoot + "/sock"
	// databaseUpgradeData is the new cluster pg_upgrade fills, and
	// databaseUpgradeRetired the old one once switched out.
	databaseUpgradeData    = databaseMountPoint + "/data.upgrade"
	databaseUpgradeRetired = databaseMountPoint + "/data.retired"
	// databaseUpgradeMarker records a successful pg_upgrade.
	databaseUpgradeMarker = databaseMountPoint + "/upgraded"
	// databaseUpgradeExtensions is the script pg_upgrade writes when an
	// extension needs ALTER EXTENSION ... UPDATE in the new major.
	databaseUpgradeExtensions = databaseMountPoint + "/update_extensions.sql"
	// databaseUpgradeAnalyze marks an upgraded cluster whose statistics
	// are still to rebuild: pg_upgrade carries none over, and the database
	// may park before the pass ends, so every boot resumes it.
	databaseUpgradeAnalyze = databaseMountPoint + "/upgrade_analyze"
	// databaseCleanShutdown is pg_controldata's state of a cluster shut
	// down cleanly.
	databaseCleanShutdown = "shut down"
)

// upgradeOldBinDir is the old major's binaries, on the mounted old base.
// PostgreSQL finds its share and library directories relative to its
// binaries, so they resolve on the old base too.
func upgradeOldBinDir(from int) string {
	return databaseUpgradeOldBase + databaseBinDir(from)
}

// upgradeInitdbArgs initializes the new cluster as databaseInitdbArgs
// does, with the old cluster's data checksums: pg_upgrade needs them to
// match, and PostgreSQL 18 turned them on by default.
func upgradeInitdbArgs(to int, checksums bool) []string {
	args := databaseInitdbArgs(to)
	for i, arg := range args {
		if arg == databasePGData {
			args[i] = databaseUpgradeData
		}
	}
	switch {
	case checksums:
		args = append(args, "--data-checksums")
	case to >= 18:
		args = append(args, "--no-data-checksums")
	}
	return args
}

// pgUpgradeArgs runs pg_upgrade from the old major's cluster in
// databasePGData to the new one in databaseUpgradeData, linking files;
// check only checks.
func pgUpgradeArgs(from, to, jobs int, check bool) []string {
	args := []string{
		databaseBinDir(to) + "/pg_upgrade",
		"--old-bindir=" + upgradeOldBinDir(from),
		"--new-bindir=" + databaseBinDir(to),
		"--old-datadir=" + databasePGData,
		"--new-datadir=" + databaseUpgradeData,
		"--username=" + databaseOSUser,
		"--socketdir=" + databaseUpgradeSocketDir,
		"--link",
		"--jobs=" + strconv.Itoa(max(jobs, 1)),
	}
	if check {
		args = append(args, "--check")
	}
	return args
}

// controlDataChecksums reports from pg_controldata's output whether the
// cluster has data checksums.
func controlDataChecksums(out string) (bool, bool) {
	value, ok := controlDataField(out, "Data page checksum version")
	return ok && value != "0", ok
}

// controlDataField reads one field of pg_controldata's output.
func controlDataField(out, field string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), field+":"); ok {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// recoveryServerArgs runs the old major's postgres on the old cluster, as
// a primary that archives, for a crashed cluster's recovery.
func recoveryServerArgs(from int, memTotalKiB int64) []string {
	args := databaseServerArgs(from, memTotalKiB, true)
	args[0] = upgradeOldBinDir(from) + "/postgres"
	return args
}

// pendingWAL lists the WAL files an archive_status directory still has
// waiting for the archiver, in name order.
func pendingWAL(status []string) []string {
	var names []string
	for _, entry := range status {
		if name, ok := strings.CutSuffix(entry, ".ready"); ok && dbarchive.ValidWALName(name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
