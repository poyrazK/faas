package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// --postgres binds DATABASE_URL and MIGRATION_DATABASE_URL; an env file
// carrying either would be synced as a customer secret that the managed
// binding cannot replace, so the CLI refuses before contacting Gregale.
func TestDevPostgresRefusesEnvFileDatabaseKeys(t *testing.T) {
	for _, key := range []string{"DATABASE_URL", "MIGRATION_DATABASE_URL"} {
		t.Run(key, func(t *testing.T) {
			main.SetupConnectionProfiles(t)
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "dev.env"), []byte(key+"=postgres://u:p@db.example/app\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, stderr, restore := main.SwapIO(t)
			code := main.Run([]string{"dev", "--path", ".", "--name", "demo", "--postgres", "--env-file", "dev.env", "--once"})
			errorOutput := stderr()
			restore()
			if code == 0 || !strings.Contains(errorOutput, "--postgres provides "+key) {
				t.Fatalf("exit=%d stderr=%q, want the %s refusal", code, errorOutput, key)
			}
			if strings.Contains(errorOutput, "u:p@") {
				t.Fatal("refusal leaked the env file value")
			}
		})
	}
}
