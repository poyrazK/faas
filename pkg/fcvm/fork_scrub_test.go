// adr: 732
package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScrubDriveSecretsRemovesOnlySecretProjections(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	write("upper/etc/faas/secrets.env", `{"DATABASE_URL":"postgres://secret"}`)
	write("upper/tmp/gregale-secret-reload/secrets.json", `{"API_KEY":"secret"}`)
	write("upper/tmp/gregale-secret-reload/snapshot.json", `{}`)
	write("upper/etc/faas/env.json", `{"MODE":"plain"}`)
	write("upper/srv/app/state.db", "app data")
	// A sidecar's env overrides hold its unsealed sealed env and app secrets
	// (Manager.prepareSidecarEnv), on the main workload's drive1.
	write("upper/etc/faas/workloads/worker/env.json", `{"STRIPE_KEY":"sk_live_secret"}`)
	write("upper/etc/faas/workloads/worker/workload.json", `{"name":"worker"}`)
	// The drive is tenant-writable: a link planted under the workloads
	// directory must not make the scrub delete a file outside the drive.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "env.json"), []byte("host file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "upper/etc/faas/workloads/escape")); err != nil {
		t.Fatal(err)
	}

	for range 2 { // idempotent: the second pass finds nothing to remove
		if err := scrubDriveSecrets(context.Background(), &dirDriveImage{root: root}); err != nil {
			t.Fatalf("scrubDriveSecrets: %v", err)
		}
	}
	for _, gone := range []string{"upper/etc/faas/secrets.env", "upper/tmp/gregale-secret-reload", "upper/etc/faas/workloads/worker/env.json"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still present (err=%v)", gone, err)
		}
	}
	for _, kept := range []string{"upper/etc/faas/env.json", "upper/srv/app/state.db", "upper/etc/faas/workloads/worker/workload.json"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "env.json")); err != nil {
		t.Errorf("scrub followed a planted link out of the drive: %v", err)
	}
}

func TestScrubDriveSecretsWithoutSidecars(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "upper/etc/faas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := scrubDriveSecrets(context.Background(), &dirDriveImage{root: root}); err != nil {
		t.Fatalf("scrubDriveSecrets on a drive with no secrets or sidecars: %v", err)
	}
}
