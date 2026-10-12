package e2etest

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
)

func TestPostgresGuestManifestCoversHarnessAndRuntimeArtifacts(t *testing.T) {
	names := append(append([]string(nil), DaemonBinaries...), StaticHelperBinaries...)
	names = append(names, "guest_init", "kernel", "builder_base", "firecracker", "jailer", "e2e_test", "probe_base", "node22_base")
	contract := guestqualification.SuiteContract()
	if len(names) != len(contract.Artifacts) {
		t.Fatal("guest contract omits a harness artifact")
	}
	seen := make(map[string]bool)
	for _, name := range contract.Artifacts {
		seen[name] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("guest contract omits %s", name)
		}
	}
	path := filepath.Join(t.TempDir(), "base")
	if err := os.WriteFile(path, []byte("first base"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := PostgresGuestArtifactDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement base"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := PostgresGuestArtifactDigest(path)
	if err != nil || first == second {
		t.Fatal("base replacement not distinguished")
	}
	if _, err := PostgresGuestArtifactDigest(filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted as runtime artifact")
	}
}

func TestPostgresGuestCachedBinaryMustMatchCleanNativeRevision(t *testing.T) {
	revision := strings.Repeat("a", 40)
	settings := map[string]string{"vcs": "git", "vcs.revision": revision, "vcs.modified": "false", "GOOS": "linux", "GOARCH": "amd64"}
	build := func(values map[string]string) *debug.BuildInfo {
		b := &debug.BuildInfo{}
		for k, v := range values {
			b.Settings = append(b.Settings, debug.BuildSetting{Key: k, Value: v})
		}
		return b
	}
	if !postgresGuestBinaryRevision(build(settings), revision) {
		t.Fatal("valid native binary rejected")
	}
	for _, key := range []string{"vcs.revision", "vcs.modified", "GOOS", "GOARCH"} {
		t.Run(key, func(t *testing.T) {
			candidate := make(map[string]string)
			for k, v := range settings {
				candidate[k] = v
			}
			candidate[key] = "wrong"
			if postgresGuestBinaryRevision(build(candidate), revision) {
				t.Fatal("foreign/dirty cached binary accepted")
			}
		})
	}
	if postgresGuestBinaryRevision(&debug.BuildInfo{}, revision) {
		t.Fatal("unstamped cached binary accepted")
	}
}
