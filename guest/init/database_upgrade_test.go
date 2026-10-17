// adr: 1027 — an in-place major upgrade links the old cluster into a new
// one with matching checksums, running the old major's binaries from its
// base on the third drive.
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
