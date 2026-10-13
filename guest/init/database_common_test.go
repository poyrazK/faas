// adr: 1027
package main

import (
	"slices"
	"strings"
	"testing"
)

func TestParseMemTotalKiB(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "typical", in: "MemTotal:         386412 kB\nMemFree: 1 kB\n", want: 386412},
		{name: "not first line", in: "Foo: 1 kB\nMemTotal: 1024 kB\n", want: 1024},
		{name: "missing", in: "MemFree: 1 kB\n", wantErr: true},
		{name: "garbage", in: "MemTotal: lots kB\n", wantErr: true},
		{name: "zero", in: "MemTotal: 0 kB\n", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMemTotalKiB([]byte(tc.in))
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("parseMemTotalKiB = %d, %v; want %d, err=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestDatabaseSharedBuffersMB(t *testing.T) {
	cases := []struct {
		memKiB int64
		want   int64
	}{
		{memKiB: 32 * 1024, want: 16},          // tiny guest floors at 16 MB
		{memKiB: 384 * 1024, want: 96},         // dev-class guest
		{memKiB: 4 * 1024 * 1024, want: 1024},  // 4 GB
		{memKiB: 64 * 1024 * 1024, want: 8192}, // capped
	}
	for _, tc := range cases {
		if got := databaseSharedBuffersMB(tc.memKiB); got != tc.want {
			t.Fatalf("databaseSharedBuffersMB(%d) = %d, want %d", tc.memKiB, got, tc.want)
		}
	}
}

// TestDatabaseServerArgsPinSafetySettings pins the settings that must hold
// in every database VM regardless of the tenant's postgresql.conf.
func TestDatabaseServerArgsPinSafetySettings(t *testing.T) {
	args := databaseServerArgs(17, 384*1024, false)
	if args[0] != "/usr/lib/postgresql/17/bin/postgres" || args[1] != "-D" || args[2] != databasePGData {
		t.Fatalf("unexpected binary or data dir: %v", args[:3])
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--single-transaction") {
		t.Fatalf("psql must not wrap admin batches; CREATE DATABASE cannot run in a transaction: %s", joined)
	}
	for _, want := range []string{
		"-c listen_addresses= ",
		"-c unix_socket_directories=/run/postgresql",
		"-c shared_buffers=96MB",
		"-c jit=off",
		"-c huge_pages=off",
		"-c password_encryption=scram-sha-256",
	} {
		if !strings.Contains(joined+" ", want) {
			t.Errorf("server args missing %q in %q", want, joined)
		}
	}
}

// adr: 1027 — archiving is on only when the host relays it; otherwise
// archive_mode would grow pg_wal without bound.
func TestDatabaseServerArgsArchiveOnlyWhenRelayed(t *testing.T) {
	off := strings.Join(databaseServerArgs(17, 384*1024, false), " ")
	if strings.Contains(off, "archive_") {
		t.Fatalf("archiving configured without a relay: %s", off)
	}
	on := strings.Join(databaseServerArgs(17, 384*1024, true), " ")
	for _, want := range []string{"-c archive_mode=on", "-c archive_command=/sbin/init archive-wal put %p %f", "-c archive_timeout=60s"} {
		if !strings.Contains(on, want) {
			t.Errorf("archiving args missing %q in %q", want, on)
		}
	}
}

func TestDatabaseInitdbArgsCarryNoSecret(t *testing.T) {
	args := databaseInitdbArgs(16)
	if args[0] != "/usr/lib/postgresql/16/bin/initdb" {
		t.Fatalf("initdb binary = %q", args[0])
	}
	for _, want := range []string{"--auth-local=peer", "--auth-host=reject", "-U", "postgres", "--encoding=UTF8"} {
		if !slices.Contains(args, want) {
			t.Errorf("initdb args missing %q: %v", want, args)
		}
	}
	for _, arg := range args {
		if strings.Contains(arg, "trust") || strings.Contains(arg, "pwfile") {
			t.Fatalf("initdb must not use trust or a password file: %v", args)
		}
	}
}

// TestDatabaseHBAKeepsSuperuserLocal pins the authentication policy: the
// superuser only by peer, everyone else by SCRAM, nothing trusted.
func TestDatabaseHBAKeepsSuperuserLocal(t *testing.T) {
	var rules []string
	for _, line := range strings.Split(databaseHBA, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			rules = append(rules, strings.Join(strings.Fields(line), " "))
		}
	}
	want := []string{"local all postgres peer", "local all all scram-sha-256"}
	if !slices.Equal(rules, want) {
		t.Fatalf("pg_hba rules = %q, want %q", rules, want)
	}
}

func TestDatabasePsqlArgs(t *testing.T) {
	args := databasePsqlArgs(17)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--single-transaction") {
		t.Fatalf("psql must not wrap admin batches; CREATE DATABASE cannot run in a transaction: %s", joined)
	}
	for _, want := range []string{"/usr/lib/postgresql/17/bin/psql -X", "ON_ERROR_STOP=1", "VERBOSITY=sqlstate", "-U postgres", "-f -"} {
		if !strings.Contains(joined, want) {
			t.Errorf("psql args missing %q: %s", want, joined)
		}
	}
}

func TestParseSQLState(t *testing.T) {
	cases := map[string]string{
		"psql:<stdin>:1: ERROR:  42710\n":              "42710",
		"psql:<stdin>:3: ERROR:  42P01\nother noise\n": "42P01",
		"psql: error: connection to server failed\n":   "",
		"": "",
	}
	for in, want := range cases {
		if got := parseSQLState([]byte(in)); got != want {
			t.Errorf("parseSQLState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClusterMajor(t *testing.T) {
	if got, err := clusterMajor([]byte("17\n")); err != nil || got != 17 {
		t.Fatalf("clusterMajor(17) = %d, %v", got, err)
	}
	for _, bad := range []string{"", "x", "-1", "0"} {
		if _, err := clusterMajor([]byte(bad)); err == nil {
			t.Fatalf("clusterMajor(%q) accepted", bad)
		}
	}
}

func TestPostmasterReady(t *testing.T) {
	ready := "123\n/var/lib/postgresql/data\n1700000000\n5432\n/run/postgresql\n\n  1234  5678\nready   \n"
	starting := strings.Replace(ready, "ready   ", "starting", 1)
	if !postmasterReady([]byte(ready)) {
		t.Fatal("ready pid file not recognized")
	}
	if postmasterReady([]byte(starting)) || postmasterReady([]byte("123\n")) || postmasterReady(nil) {
		t.Fatal("non-ready pid file reported ready")
	}
}

func TestLookupPasswdUser(t *testing.T) {
	passwd := []byte("root:x:0:0:root:/root:/bin/bash\npostgres:x:999:999::/var/lib/postgresql:/bin/bash\nbad:x:a:b::/:/bin/sh\n")
	if uid, gid, ok := lookupPasswdUser(passwd, "postgres"); !ok || uid != 999 || gid != 999 {
		t.Fatalf("postgres = %d:%d ok=%v", uid, gid, ok)
	}
	if _, _, ok := lookupPasswdUser(passwd, "root"); ok {
		t.Fatal("uid 0 must not be accepted as the database user")
	}
	if _, _, ok := lookupPasswdUser(passwd, "bad"); ok {
		t.Fatal("non-numeric ids accepted")
	}
	if _, _, ok := lookupPasswdUser(passwd, "missing"); ok {
		t.Fatal("missing user found")
	}
}
