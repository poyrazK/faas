//go:build linux

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestFetchRuntimeSecretsUsesDedicatedRequestKind(t *testing.T) {
	previous := dialRuntimeConfigHost
	t.Cleanup(func() { dialRuntimeConfigHost = previous })
	dialRuntimeConfigHost = func() (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			body, err := readRuntimeConfigFrame(server)
			if err != nil {
				return
			}
			var request runtimeConfigRequest
			if json.Unmarshal(body, &request) != nil || request.Kind != "secrets" || request.Revision != "" {
				return
			}
			_ = writeRuntimeConfigFrame(server, []byte(`{"secrets":{"DB_URL":"postgres://new"},"revision":"r2"}`))
		}()
		return client, nil
	}
	response, err := fetchRuntimeSecrets("")
	if err != nil {
		t.Fatal(err)
	}
	if response.Secrets == nil || (*response.Secrets)["DB_URL"] != "postgres://new" || response.Revision != "r2" {
		t.Fatalf("runtime secrets response = %+v", response)
	}
}

func TestSendRuntimeSecretReloadReportUsesClosedMetadata(t *testing.T) {
	previous := dialRuntimeConfigHost
	t.Cleanup(func() { dialRuntimeConfigHost = previous })
	dialRuntimeConfigHost = func() (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			body, err := readRuntimeConfigFrame(server)
			if err != nil {
				return
			}
			var request runtimeConfigRequest
			if json.Unmarshal(body, &request) != nil || request.Kind != "secret_reload_status" ||
				request.Revision != strings.Repeat("a", 64) || request.Projection != "updated" ||
				request.Signal != "sent" || request.ErrorCode != "" {
				return
			}
			_ = writeRuntimeConfigFrame(server, []byte(`{"accepted":true}`))
		}()
		return client, nil
	}
	accepted, stale, err := sendRuntimeSecretReloadReport(runtimeSecretReloadReport{
		Revision: strings.Repeat("a", 64), Projection: "updated", Signal: "sent",
	})
	if err != nil || !accepted || stale {
		t.Fatalf("sendRuntimeSecretReloadReport = accepted %t stale %t err %v", accepted, stale, err)
	}
}

func TestWriteRuntimeSecretsProjectionIsAtomicAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projection", "secrets.json")
	uid, gid := os.Getuid(), os.Getgid()
	if err := writeRuntimeSecretsProjectionForOwner(path, uid, uid, gid, map[string]string{"DB_URL": "old"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("projection mode = %#o, want 0400", info.Mode().Perm())
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != uid {
		t.Fatalf("projection owner = %#v, want uid %d", info.Sys(), uid)
	}
	if err := writeRuntimeSecretsProjectionForOwner(path, uid, uid, gid, map[string]string{"DB_URL": "new", "TOKEN": "next"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["DB_URL"] != "new" || got["TOKEN"] != "next" {
		t.Fatalf("projection = %#v", got)
	}
}

func TestRuntimeSecretsStateCopiesValues(t *testing.T) {
	initial := map[string]string{"A": "one"}
	state := newRuntimeSecretsState(initial)
	initial["A"] = "mutated"
	snapshot := state.snapshot()
	snapshot["A"] = "also-mutated"
	if got := state.snapshot()["A"]; got != "one" {
		t.Fatalf("state snapshot alias = %q, want original value", got)
	}
}

func TestSecretReloadSyscallClosedSet(t *testing.T) {
	if got := secretReloadSyscall("SIGUSR1"); got != syscall.SIGUSR1 {
		t.Fatalf("SIGUSR1 mapped to %v", got)
	}
	if got := secretReloadSyscall("SIGUSR2"); got != syscall.SIGUSR2 {
		t.Fatalf("SIGUSR2 mapped to %v", got)
	}
	if got := secretReloadSyscall("SIGHUP"); got != syscall.SIGHUP {
		t.Fatalf("SIGHUP mapped to %v", got)
	}
}

func TestValidGuestRuntimeSecretRevision(t *testing.T) {
	if !validGuestRuntimeSecretRevision(strings.Repeat("a", 64)) {
		t.Fatal("64-character SHA-256 hex revision rejected")
	}
	for _, revision := range []string{"", "r2", strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if validGuestRuntimeSecretRevision(revision) {
			t.Errorf("invalid revision %q accepted", revision)
		}
	}
}
