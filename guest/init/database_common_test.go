// adr: 1027
package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	want := []string{"local all postgres peer", "local replication postgres peer", "local all all scram-sha-256"}
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

// adr: 1027 — a base backup can only write regular files and directories
// inside the data directory.
func TestExtractBaseTarRefusesEscapesAndLinks(t *testing.T) {
	build := func(entries ...tar.Header) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range entries {
			h := h
			body := ""
			if h.Typeflag == tar.TypeReg {
				body = "x"
				h.Size = 1
			}
			if err := tw.WriteHeader(&h); err != nil {
				t.Fatal(err)
			}
			_, _ = tw.Write([]byte(body))
		}
		_ = tw.Close()
		return buf.Bytes()
	}
	uid, gid := os.Getuid(), os.Getgid()
	good := build(tar.Header{Name: "base/", Typeflag: tar.TypeDir, Mode: 0o700}, tar.Header{Name: "base/1/2", Typeflag: tar.TypeReg, Mode: 0o600}, tar.Header{Name: "PG_VERSION", Typeflag: tar.TypeReg, Mode: 0o600})
	dir := t.TempDir()
	if err := extractBaseTar(bytes.NewReader(good), dir, uid, gid); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "base", "1", "2")); err != nil || string(body) != "x" {
		t.Fatalf("extracted = %q, %v", body, err)
	}
	for name, bad := range map[string][]byte{
		"parent escape": build(tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o600}),
		"nested escape": build(tar.Header{Name: "base/../../evil", Typeflag: tar.TypeReg, Mode: 0o600}),
		"absolute":      build(tar.Header{Name: "/etc/evil", Typeflag: tar.TypeReg, Mode: 0o600}),
		"symlink":       build(tar.Header{Name: "pg_wal", Typeflag: tar.TypeSymlink, Linkname: "/etc"}),
		"hard link":     build(tar.Header{Name: "x", Typeflag: tar.TypeLink, Linkname: "PG_VERSION"}),
	} {
		if err := extractBaseTar(bytes.NewReader(bad), t.TempDir(), uid, gid); err == nil {
			t.Errorf("%s: extracted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
		t.Fatal("a file escaped the data directory")
	}
}

func TestRecoverySettingsTargetAndPromote(t *testing.T) {
	got := strings.Join(recoverySettings(time.Date(2026, 10, 13, 9, 30, 15, 250000000, time.UTC)), " ")
	for _, want := range []string{
		"-c restore_command=/sbin/init archive-wal get %f %p",
		"-c recovery_target_time=2026-10-13 09:30:15.25+00",
		"-c recovery_target_action=promote",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("recovery settings missing %q in %q", want, got)
		}
	}
}

func TestArchivePendingCountsReadyFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"000000010000000000000001.done", "000000010000000000000002.ready", "00000002.history.ready"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := archivePending(dir); err != nil || n != 2 {
		t.Fatalf("pending = %d, %v; want 2", n, err)
	}
	if _, err := archivePending(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing status directory counted as drained")
	}
}

func TestRecoverySettingsWithoutTargetRecoverToTheEnd(t *testing.T) {
	got := strings.Join(recoverySettings(time.Time{}), " ")
	if !strings.Contains(got, "restore_command=") || strings.Contains(got, "recovery_target") {
		t.Fatalf("untargeted recovery settings = %q", got)
	}
}

func TestParseReplayTimestamp(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2026-10-13 09:30:15.123456+00": time.Date(2026, 10, 13, 9, 30, 15, 123456000, time.UTC),
		"2026-10-13 09:30:15+00":        time.Date(2026, 10, 13, 9, 30, 15, 0, time.UTC),
		"2026-10-13 11:30:15.5+02":      time.Date(2026, 10, 13, 9, 30, 15, 500000000, time.UTC),
		"":                              {},
	} {
		got, err := parseReplayTimestamp(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("parse %q = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseReplayTimestamp("yesterday"); err == nil {
		t.Error("garbage parsed")
	}
}
