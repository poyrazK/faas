package main

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestCleanupBaseScratchRemovesOnlyControllerTempFiles(t *testing.T) {
	if _, err := user.Lookup("faas-imaged"); err != nil {
		t.Skip("host has no faas-imaged user; cleanup contract is production-only")
	}
	root := t.TempDir()
	remove := filepath.Join(root, "faas-base-mkfs-old.ext4")
	keepFile := filepath.Join(root, "customer.ext4")
	keepDir := filepath.Join(root, "faas-base-mkfs-active")
	for _, path := range []string{remove, keepFile} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(keepDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cleanupBaseScratch(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(remove); !os.IsNotExist(err) {
		t.Fatalf("controller temp file still exists: %v", err)
	}
	for _, path := range []string{keepFile, keepDir} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected path %s changed: %v", path, err)
		}
	}
}

// TestCleanupBaseScratchRemovesExtractionDirsAndPreservesForeignOwned
// covers the expanded contract: faas-base-* extraction DIRECTORIES are
// removed (not just mkfs .ext4 files), but only when owned by the
// faas-imaged service user — a foreign-owned directory with the same
// name prefix is preserved. The ownership filter is the defence
// against deleting an operator's artifact that happens to match the
// controller name pattern.
func TestCleanupBaseScratchRemovesExtractionDirsAndPreservesForeignOwned(t *testing.T) {
	owner, err := user.Lookup("faas-imaged")
	if err != nil {
		t.Skip("host has no faas-imaged user; ownership filter untestable here")
	}
	ownerUID, err := strconv.Atoi(owner.Uid)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ownedDir := filepath.Join(root, "faas-base-12345")
	if err := os.Mkdir(ownedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(ownedDir, ownerUID, -1); err != nil {
		t.Skip("chown to faas-imaged requires root; ownership filter untestable here")
	}
	foreignDir := filepath.Join(root, "faas-base-foreign")
	if err := os.Mkdir(foreignDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreignFile := filepath.Join(root, "faas-base-mkfs-foreign.ext4")
	if err := os.WriteFile(foreignFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cleanupBaseScratch(root); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(ownedDir); !os.IsNotExist(err) {
		t.Fatalf("owned extraction dir still exists: %v", err)
	}
	for _, path := range []string{foreignDir, foreignFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("foreign-owned path %s was removed: %v", path, err)
		}
	}
}

// TestEnsureBaseStagingRootsOwnership validates that the two staging
// roots are created with the faas-imaged service ownership (so imaged
// can write faas-base-* temp dirs inside them under
// ProtectSystem=strict). Requires root + the service user to exist;
// skipped otherwise.
func TestEnsureBaseStagingRootsOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown requires root")
	}
	svcUser, err := user.Lookup("faas-imaged")
	if err != nil {
		t.Skip("host has no faas-imaged user")
	}
	svcGroup, err := user.LookupGroup("faas")
	if err != nil {
		t.Skip("host has no faas group")
	}
	uid, _ := strconv.Atoi(svcUser.Uid)
	gid, _ := strconv.Atoi(svcGroup.Gid)

	for _, root := range baseStagingRoots {
		info, err := os.Stat(root)
		if err != nil {
			t.Fatalf("stat %s: %v", root, err)
		}
		sys, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("%s: unexpected stat type", root)
		}
		if int(sys.Uid) != uid || int(sys.Gid) != gid {
			t.Errorf("%s owned by %d:%d, want %d:%d", root, sys.Uid, sys.Gid, uid, gid)
		}
	}
}

// TestActivateObservabilityInstallsBundleAssets verifies that the
// release bundle's observability files are installed into the runtime
// paths atomically. It uses a temp runtime dir via the hostRuntime
// unitDir seam and a release root that mirrors the CI bundle layout;
// the systemd reload is exercised only where systemd exists.
func TestActivateObservabilityInstallsBundleAssets(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("installing /etc/prometheus requires root; file-install contract covered by CI bundle + runtime on the host")
	}
	if _, err := os.Stat("/etc/systemd/system"); err != nil {
		t.Skip("no systemd unit dir on this host")
	}
	if _, err := os.Stat("/usr/local/bin/promtool"); err != nil {
		t.Skip("promtool not installed; validation step requires it")
	}

	releaseRoot := t.TempDir()
	obs := filepath.Join(releaseRoot, "observability")
	if err := os.MkdirAll(obs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"prometheus.yml", "faas.rules.yml", "pg_backup.rules.yml", "prometheus.service", "alertmanager.service"} {
		if err := os.WriteFile(filepath.Join(obs, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := hostRuntime{unitDir: t.TempDir()}
	if err := r.activateObservability(context.Background(), releaseRoot); err != nil {
		t.Fatalf("activateObservability: %v", err)
	}
	for _, f := range []string{"prometheus.yml", "faas.rules.yml", "pg_backup.rules.yml"} {
		if _, err := os.Stat(filepath.Join("/etc/prometheus", f)); err != nil {
			t.Errorf("expected /etc/prometheus/%s installed: %v", f, err)
		}
	}
}
