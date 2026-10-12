package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Database-mode layout (ADR-1027). drive0 is the read-only base carrying
// the PostgreSQL binaries; drive1 is the database's persistent volume,
// formatted ext4 by the host when the volume is created.
const (
	databaseVolumeDevice = "/dev/vdb"
	databaseMountPoint   = "/var/lib/postgresql"
	databasePGData       = databaseMountPoint + "/data"
	databaseSocketDir    = "/run/postgresql"
	databaseOSUser       = "postgres"
	// databaseMaxConnections stays small: clients reach the database
	// through the platform proxy, which pools connections.
	databaseMaxConnections = 25
	// databaseMaxWALSize bounds crash recovery after an unclean stop.
	databaseMaxWALSize = "256MB"
)

func databaseBinDir(major int) string {
	return fmt.Sprintf("/usr/lib/postgresql/%d/bin", major)
}

// parseMemTotalKiB reads MemTotal from /proc/meminfo content.
func parseMemTotalKiB(meminfo []byte) (int64, error) {
	scanner := bufio.NewScanner(bytes.NewReader(meminfo))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kib, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || kib <= 0 {
				return 0, fmt.Errorf("meminfo: bad MemTotal %q", fields[1])
			}
			return kib, nil
		}
	}
	return 0, errors.New("meminfo: MemTotal not found")
}

// databaseSharedBuffersMB sizes shared_buffers at a quarter of guest RAM,
// between 16 MB and 8 GB. The host page cache serves the rest (ADR-1027).
func databaseSharedBuffersMB(memTotalKiB int64) int64 {
	mb := memTotalKiB / 1024 / 4
	if mb < 16 {
		return 16
	}
	if mb > 8192 {
		return 8192
	}
	return mb
}

// databaseServerArgs builds the postgres command line. Settings that must
// hold for every database VM are passed as -c flags so a tenant's
// postgresql.conf cannot weaken them: no TCP listener, the socket in
// /run, no JIT.
func databaseServerArgs(major int, memTotalKiB int64) []string {
	settings := []string{
		"listen_addresses=",
		"unix_socket_directories=" + databaseSocketDir,
		fmt.Sprintf("shared_buffers=%dMB", databaseSharedBuffersMB(memTotalKiB)),
		"max_connections=" + strconv.Itoa(databaseMaxConnections),
		"max_wal_size=" + databaseMaxWALSize,
		"huge_pages=off",
		"jit=off",
		"dynamic_shared_memory_type=posix",
		"password_encryption=scram-sha-256",
	}
	args := []string{databaseBinDir(major) + "/postgres", "-D", databasePGData}
	for _, s := range settings {
		args = append(args, "-c", s)
	}
	return args
}

// databaseInitdbArgs initializes the cluster without any password: the
// superuser authenticates only by peer, as the guest's postgres OS user,
// and databaseHBA then requires SCRAM for every other role.
func databaseInitdbArgs(major int) []string {
	return []string{
		databaseBinDir(major) + "/initdb",
		"-D", databasePGData,
		"-U", databaseOSUser,
		"--auth-local=peer",
		"--auth-host=reject",
		"--encoding=UTF8",
		"--locale=C.UTF-8",
		"--no-instructions",
	}
}

// databaseHBA replaces initdb's pg_hba.conf. Clients reach PostgreSQL only
// through guest-init's vsock bridge, which runs as root, so peer
// authentication can never admit them as the superuser; the superuser is
// reachable only by the admin control op running as the postgres OS user.
const databaseHBA = `# Managed by guest-init (ADR-1027). Do not edit.
local all postgres peer
local all all scram-sha-256
`

// databasePsqlArgs runs an admin batch from stdin as the superuser,
// stopping at the first error and reporting it as a SQLSTATE. Batches that
// must be atomic wrap themselves in BEGIN/COMMIT; CREATE DATABASE cannot
// run inside a transaction, so psql does not add one.
func databasePsqlArgs(major int) []string {
	return []string{
		databaseBinDir(major) + "/psql",
		"-X", "-q",
		"-v", "ON_ERROR_STOP=1",
		"-v", "VERBOSITY=sqlstate",
		"-h", databaseSocketDir,
		"-U", databaseOSUser,
		"-d", "postgres",
		"-f", "-",
	}
}

var sqlStatePattern = regexp.MustCompile(`ERROR:\s+([0-9A-Z]{5})\b`)

// parseSQLState extracts the SQLSTATE psql printed for a failed batch.
func parseSQLState(stderr []byte) string {
	if m := sqlStatePattern.FindSubmatch(stderr); m != nil {
		return string(m[1])
	}
	return ""
}

// clusterMajor parses PG_VERSION. A cluster from another major must not be
// started by these binaries.
func clusterMajor(pgVersion []byte) (int, error) {
	major, err := strconv.Atoi(strings.TrimSpace(string(pgVersion)))
	if err != nil || major <= 0 {
		return 0, fmt.Errorf("PG_VERSION: bad value %q", strings.TrimSpace(string(pgVersion)))
	}
	return major, nil
}

// postmasterReady reports whether postmaster.pid records the "ready"
// status (line 8), which PostgreSQL writes once it accepts connections.
func postmasterReady(pidFile []byte) bool {
	lines := strings.Split(string(pidFile), "\n")
	return len(lines) >= 8 && strings.TrimSpace(lines[7]) == "ready"
}

// lookupPasswdUser finds a user's uid and gid in /etc/passwd content.
func lookupPasswdUser(passwd []byte, name string) (uid, gid int, ok bool) {
	scanner := bufio.NewScanner(bytes.NewReader(passwd))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 4 || fields[0] != name {
			continue
		}
		u, uErr := strconv.Atoi(fields[2])
		g, gErr := strconv.Atoi(fields[3])
		if uErr != nil || gErr != nil || u <= 0 || g <= 0 {
			return 0, 0, false
		}
		return u, g, true
	}
	return 0, 0, false
}
