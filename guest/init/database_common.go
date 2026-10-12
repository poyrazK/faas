package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
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
	databaseBootstrapDir = "/run/gregale-bootstrap"
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

// databaseInitdbArgs initializes the cluster with SCRAM authentication for
// every connection; the superuser secret comes from a 0600 file in tmpfs.
func databaseInitdbArgs(major int, pwfile string) []string {
	return []string{
		databaseBinDir(major) + "/initdb",
		"-D", databasePGData,
		"-U", databaseOSUser,
		"--pwfile=" + pwfile,
		"--auth=scram-sha-256",
		"--encoding=UTF8",
		"--locale=C.UTF-8",
		"--no-instructions",
	}
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
