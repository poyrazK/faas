package pgtest

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise testing.T's actual skip/fatal behavior in an isolated process.
// Unit runs may omit a database, but an explicitly configured broken fixture
// must fail rather than quietly disappearing from PostgreSQL acceptance.
func TestTemplateDatabaseSetupContract(t *testing.T) {
	const childKey = "GREGALE_PGTEST_SETUP_CONTRACT_CHILD"
	if os.Getenv(childKey) == "1" {
		OpenMigrated(t)
		t.Fatal("a skipped or invalid fixture unexpectedly opened")
	}
	query := url.Values{"host": {filepath.Join(t.TempDir(), "unavailable-socket")}, "connect_timeout": {"1"}}
	unreachable := "postgres:///unused?" + query.Encode()
	for _, tc := range []struct {
		name, dsn, skip, message string
		failed                   bool
	}{
		{name: "unconfigured", message: "DATABASE_URL unset"},
		{name: "explicit_skip", dsn: "postgres://%", skip: "1", message: "FAAS_SKIP_PG_TESTS set"},
		{name: "configured_invalid", dsn: "postgres://%", message: "cannot parse configured DATABASE_URL", failed: true},
		{name: "configured_unreachable", dsn: unreachable, message: "ping template admin connection", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.v", "-test.run=^TestTemplateDatabaseSetupContract$")
			for _, env := range os.Environ() {
				if strings.HasPrefix(env, "DATABASE_URL=") || strings.HasPrefix(env, "FAAS_SKIP_PG_TESTS=") ||
					strings.HasPrefix(env, UseTemplateDatabase+"=") || strings.HasPrefix(env, childKey+"=") {
					continue
				}
				cmd.Env = append(cmd.Env, env)
			}
			cmd.Env = append(cmd.Env, childKey+"=1", UseTemplateDatabase+"=1", "DATABASE_URL="+tc.dsn, "FAAS_SKIP_PG_TESTS="+tc.skip)
			output, err := cmd.CombinedOutput()
			verdict := "--- SKIP: TestTemplateDatabaseSetupContract"
			if tc.failed {
				verdict = "--- FAIL: TestTemplateDatabaseSetupContract"
			}
			if (err != nil) != tc.failed || !strings.Contains(string(output), verdict) || !strings.Contains(string(output), tc.message) {
				t.Fatalf("wrong setup verdict: err=%v output=%s", err, output)
			}
		})
	}
}
