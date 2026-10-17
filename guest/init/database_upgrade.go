package main

import (
	"bufio"
	"strconv"
	"strings"
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
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "Data page checksum version:"); ok {
			return strings.TrimSpace(value) != "0", true
		}
	}
	return false, false
}
