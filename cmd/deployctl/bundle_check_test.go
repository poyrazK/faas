package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

func TestRunBundleCheckAcceptsInstalledSBOMBaselineOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "daemon"), []byte("release"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := releasebundle.Build(root, "release", "0123456789abcdef0123456789abcdef01234567", "linux/amd64", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Write(root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, releaseinstall.SBOMBaselineName), []byte(`{"counts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runBundleCheck([]string{root}); err == nil {
		t.Fatal("strict candidate check accepted the host-owned KGV baseline")
	}
	if err := runInstalledBundleCheck([]string{root}); err != nil {
		t.Fatalf("installed release check rejected KGV baseline: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInstalledBundleCheck([]string{root}); err == nil {
		t.Fatal("installed release check accepted an unrelated extra file")
	}
}

// TestRunBundleCreateProducesAVerifiableBundle pins the contract after
// bundle-create stopped re-hashing every file it had just hashed: the
// bundle it writes must still pass the full releasebundle.Verify that the
// control plane runs before activation.
func TestRunBundleCreateProducesAVerifiableBundle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"bin/apid": "apid", "bin/schedd": "schedd", "release.tar.gz": "tarball"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	if err := runBundleCreate([]string{root, sha, sha, "linux/amd64"}); err != nil {
		t.Fatalf("bundle-create: %v", err)
	}
	manifest, err := releasebundle.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Verify(root, manifest); err != nil {
		t.Fatalf("bundle-create output failed full verification: %v", err)
	}
	if len(manifest.Files) != 3 {
		t.Fatalf("manifest lists %d files, want 3", len(manifest.Files))
	}
}

func TestConfirmWrittenManifestRejectsAnythingButTheBuiltManifest(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	setup := func(t *testing.T) (string, releasebundle.Manifest) {
		t.Helper()
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "release.tar.gz"), []byte("tarball"), 0o644); err != nil {
			t.Fatal(err)
		}
		built, err := releasebundle.Build(root, sha, sha, "linux/amd64", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := releasebundle.Write(root, built); err != nil {
			t.Fatal(err)
		}
		return root, built
	}
	root, built := setup(t)
	if err := confirmWrittenManifest(root, built); err != nil {
		t.Fatalf("the written manifest was rejected: %v", err)
	}
	for name, corrupt := range map[string]func(t *testing.T, root string, built releasebundle.Manifest){
		"another manifest": func(t *testing.T, root string, built releasebundle.Manifest) {
			other := built
			other.ReleaseID = "fedcba9876543210fedcba9876543210fedcba98"
			if err := releasebundle.Write(root, other); err != nil {
				t.Fatal(err)
			}
		},
		"missing manifest": func(t *testing.T, root string, _ releasebundle.Manifest) {
			if err := os.Remove(filepath.Join(root, releasebundle.ManifestName)); err != nil {
				t.Fatal(err)
			}
		},
		"undecodable manifest": func(t *testing.T, root string, _ releasebundle.Manifest) {
			if err := os.WriteFile(filepath.Join(root, releasebundle.ManifestName), []byte("{"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, built := setup(t)
			corrupt(t, root, built)
			if err := confirmWrittenManifest(root, built); err == nil {
				t.Fatal("confirmWrittenManifest accepted it")
			}
		})
	}
}
