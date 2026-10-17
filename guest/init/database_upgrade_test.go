// adr: 1027 — an in-place major upgrade links the old cluster into a new
// one with matching checksums, running the old major's binaries from its
// base on the third drive; before that, a crashed old cluster recovers on
// its own binaries with archiving on, and its waiting WAL is archived.
package main

import (
	"slices"
	"strings"
	"testing"
)

func TestUpgradeArgs(t *testing.T) {
	run := strings.Join(pgUpgradeArgs(14, 17, 2, false), " ")
	for _, want := range []string{
		"/usr/lib/postgresql/17/bin/pg_upgrade",
		"--old-bindir=/run/gregale-upgrade/old/usr/lib/postgresql/14/bin",
		"--new-bindir=/usr/lib/postgresql/17/bin",
		"--old-datadir=" + databasePGData, "--new-datadir=" + databaseUpgradeData,
		"--username=postgres", "--socketdir=" + databaseUpgradeSocketDir, "--link", "--jobs=2",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("pg_upgrade lacks %s: %s", want, run)
		}
	}
	if strings.Contains(run, "--check") || !slices.Contains(pgUpgradeArgs(14, 17, 0, true), "--check") {
		t.Error("only the check checks")
	}
	if !slices.Contains(pgUpgradeArgs(14, 17, 0, false), "--jobs=1") {
		t.Error("jobs not floored at 1")
	}

	initdb := upgradeInitdbArgs(17, false)
	if !slices.Contains(initdb, databaseUpgradeData) || slices.Contains(initdb, databasePGData) || slices.Contains(initdb, "--data-checksums") {
		t.Fatalf("initdb for an unchecksummed 17 = %v", initdb)
	}
	if !slices.Contains(upgradeInitdbArgs(17, true), "--data-checksums") {
		t.Fatal("a checksummed cluster's new cluster has no checksums")
	}
	// 18 checksums by default; a cluster without them must stay so.
	if !slices.Contains(upgradeInitdbArgs(18, false), "--no-data-checksums") {
		t.Fatal("18 would turn checksums on under an unchecksummed cluster")
	}
	// The cluster's own initdb arguments are not changed underneath.
	if !slices.Contains(databaseInitdbArgs(17), databasePGData) {
		t.Fatal("upgradeInitdbArgs changed databaseInitdbArgs")
	}
}

func TestControlDataChecksums(t *testing.T) {
	for out, want := range map[string][2]bool{
		"Database cluster state:               shut down\nData page checksum version:           0\n": {false, true},
		"Data page checksum version:           1\n":                                                  {true, true},
		"pg_control version number:            1300\n":                                               {false, false},
	} {
		on, ok := controlDataChecksums(out)
		if on != want[0] || ok != want[1] {
			t.Errorf("checksums(%q) = %v, %v; want %v", out, on, ok, want)
		}
	}
}

func TestControlDataField(t *testing.T) {
	out := "pg_control version number:            1300\nDatabase cluster state:               in production\n"
	if state, ok := controlDataField(out, "Database cluster state"); !ok || state != "in production" {
		t.Fatalf("state = %q, %v", state, ok)
	}
	if state, _ := controlDataField("Database cluster state:               shut down\n", "Database cluster state"); state != databaseCleanShutdown {
		t.Fatalf("a clean shutdown reads %q", state)
	}
	// A standby's clean stop is no primary's: it is recovered too.
	if state, _ := controlDataField("Database cluster state:               shut down in recovery\n", "Database cluster state"); state == databaseCleanShutdown {
		t.Fatal("shut down in recovery read as a clean shutdown")
	}
	if _, ok := controlDataField(out, "Data page checksum version"); ok {
		t.Fatal("a missing field was found")
	}
}

func TestRecoveryServerArgs(t *testing.T) {
	args := recoveryServerArgs(14, 512<<10)
	if args[0] != "/run/gregale-upgrade/old/usr/lib/postgresql/14/bin/postgres" {
		t.Fatalf("recovery runs %s, not the old major's postgres", args[0])
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"archive_mode=on", "archive_command=" + databaseArchiveCommand, "-D " + databasePGData} {
		if !strings.Contains(joined, want) {
			t.Errorf("recovery lacks %s: %s", want, joined)
		}
	}
	// The cluster's own server arguments are not changed underneath.
	if databaseServerArgs(14, 512<<10, true)[0] != databaseBinDir(14)+"/postgres" {
		t.Fatal("recoveryServerArgs changed databaseServerArgs")
	}
}

func TestPendingWAL(t *testing.T) {
	got := pendingWAL([]string{
		"000000010000000000000004.ready", "000000010000000000000003.done", "000000010000000000000002.ready",
		"00000002.history.ready", "000000010000000000000002.00000028.backup.ready", "not-wal.ready", "../escape.ready", "000000010000000000000005",
	})
	want := []string{"000000010000000000000002", "000000010000000000000002.00000028.backup", "000000010000000000000004", "00000002.history"}
	if !slices.Equal(got, want) {
		t.Fatalf("pending = %v, want %v", got, want)
	}
}
