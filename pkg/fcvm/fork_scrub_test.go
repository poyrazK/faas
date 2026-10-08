// adr: 732
package fcvm

import (
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

	for range 2 { // idempotent: the second pass finds nothing to remove
		if err := scrubDriveSecrets(root); err != nil {
			t.Fatalf("scrubDriveSecrets: %v", err)
		}
	}
	for _, gone := range []string{"upper/etc/faas/secrets.env", "upper/tmp/gregale-secret-reload"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still present (err=%v)", gone, err)
		}
	}
	for _, kept := range []string{"upper/etc/faas/env.json", "upper/srv/app/state.db"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}
