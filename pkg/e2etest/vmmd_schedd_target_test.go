package e2etest

// Tests for vmmdEnv — the harness must point vmmd's liveness reporting at its
// own schedd, not the production socket.
//
// cmd/vmmd defaults FAAS_VMMD_SCHEDD_TARGET to unix:///run/faas/schedd.sock.
// Under the native gate that path is guaranteed absent: the production
// daemons are stopped for the run, and every harness daemon listens on a
// per-test socket. vmmd's liveness loop then failed with
//
//	liveness_conn_err: dial unix /run/faas/schedd.sock:
//	  connect: no such file or directory
//
// and tore down builder microVMs that had cold-booted correctly
// (cold_boot_ms=40, total_ms=107) with exit_code=-1. Downstream that reads as
// "build exited -1" with a zero-byte build log — a broken build, rather than a
// health probe dialling the wrong address.

import (
	"strings"
	"testing"
)

func envValue(t *testing.T, env []string, name string) (string, bool) {
	t.Helper()
	prefix := name + "="
	value, found := "", false
	// Last assignment wins, matching exec.Cmd.
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			value, found = strings.TrimPrefix(kv, prefix), true
		}
	}
	return value, found
}

func TestVMMDEnv_PointsScheddTargetAtTheHarnessSocket(t *testing.T) {
	const scheddSock = "/tmp/faas-e2e-sock-12345/schedd.sock"

	got, ok := envValue(t, vmmdEnv("postgres:///faas_e2e", "/tmp/vmmd.toml", scheddSock),
		"FAAS_VMMD_SCHEDD_TARGET")
	if !ok {
		t.Fatal("FAAS_VMMD_SCHEDD_TARGET is absent; vmmd would fall back to the " +
			"production socket, which the gate has stopped")
	}
	if want := "unix://" + scheddSock; got != want {
		t.Errorf("FAAS_VMMD_SCHEDD_TARGET = %q, want %q", got, want)
	}
	// The specific failure being pinned: anything under the production run
	// directory cannot exist while the gate holds the node.
	if strings.Contains(got, "/run/faas/") {
		t.Errorf("FAAS_VMMD_SCHEDD_TARGET = %q points into the production run directory", got)
	}
	// wire.DialContext requires a scheme; a bare path fails to dial in a way
	// that looks exactly like the bug this fixes.
	if !strings.HasPrefix(got, "unix://") {
		t.Errorf("FAAS_VMMD_SCHEDD_TARGET = %q lacks the unix:// scheme", got)
	}
}

// A configuration with no schedd must leave the variable unset rather than
// injecting an empty target, which would dial nothing.
func TestVMMDEnv_OmitsScheddTargetWithoutAHarnessSchedd(t *testing.T) {
	got, ok := envValue(t, vmmdEnv("postgres:///faas_e2e", "/tmp/vmmd.toml", ""),
		"FAAS_VMMD_SCHEDD_TARGET")
	if ok {
		t.Errorf("FAAS_VMMD_SCHEDD_TARGET = %q was set without a harness schedd", got)
	}
}

// The config path is what makes vmmd read per-test sockets and paths instead
// of /etc/faas/vmmd.toml, so it must survive alongside the new variable.
func TestVMMDEnv_KeepsTheConfigPath(t *testing.T) {
	const cfg = "/tmp/faas-e2e-abc/vmmd.toml"

	got, ok := envValue(t, vmmdEnv("postgres:///faas_e2e", cfg, "/tmp/s/schedd.sock"),
		"FAAS_VMMD_CONFIG")
	if !ok || got != cfg {
		t.Errorf("FAAS_VMMD_CONFIG = %q, %v; want %q", got, ok, cfg)
	}
}

// vmmdEnv builds on testEnvCommon, so the storage forwarding added for #2568
// must still reach vmmd — without it vmmd reads an empty /srv/fc/scans and
// refuses every cold boot.
func TestVMMDEnv_StillCarriesForwardedStorageConfiguration(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")

	got, ok := envValue(t, vmmdEnv("postgres:///faas_e2e", "/tmp/vmmd.toml", "/tmp/s/schedd.sock"),
		"FAAS_STORAGE_BACKEND")
	if !ok || got != "oci" {
		t.Errorf("FAAS_STORAGE_BACKEND = %q, %v; want oci — vmmd must resolve "+
			"artifacts through the node's backend", got, ok)
	}
}
