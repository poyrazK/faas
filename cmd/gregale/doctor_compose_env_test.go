package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-19: doctor warned DATABASE_URL was missing in a Compose
// project whose compose.yaml set it and opted in to env import.
func TestDoctorEnvRequiredCountsComposeImportedValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")

	const source = "const db = process.env.DATABASE_URL; const host = process.env.FROM_HOST;\n"
	for _, tc := range []struct {
		name, compose string
		missing       []string
	}{
		{"opted in", "x-gregale-env-import: true\nservices:\n  web:\n    build: .\n    environment:\n      DATABASE_URL: postgres://app:pw@db:5432/app\n      FROM_HOST:\n", []string{"FROM_HOST"}},
		{"not opted in", "services:\n  web:\n    build: .\n    environment:\n      DATABASE_URL: postgres://app:pw@db:5432/app\n", []string{"DATABASE_URL", "FROM_HOST"}},
	} {
		dir := t.TempDir()
		main.WriteFile(t, dir, "package.json", "{}")
		main.WriteFile(t, dir, "server.js", source)
		main.WriteFile(t, dir, "compose.yaml", tc.compose)
		_, check := doctorEnvCheck(t, dir)
		if got := strings.Join(check.Sources, ","); got != strings.Join(tc.missing, ",") {
			t.Fatalf("%s: missing = %q, want %q (check %+v)", tc.name, got, tc.missing, check)
		}
	}
}
