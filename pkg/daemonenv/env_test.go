package daemonenv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestLoadFromMissingRequiredNames(t *testing.T) {
	env, err := LoadFrom("imaged", mapLookup(map[string]string{
		"FAAS_DATABASE_URL":                 "postgres://localhost/faas",
		"FAAS_FUNCTION_RUNNER_NODE22":       "/bin/sh",
		"FAAS_FUNCTION_RUNNER_NODE24":       "/bin/sh",
		"FAAS_FUNCTION_RUNNER_PYTHON312":    "/bin/sh",
		"FAAS_FUNCTION_RUNNER_PYTHON313":    "/bin/sh",
		"FAAS_FUNCTION_RUNNER_GO124":        "/bin/sh",
		"FAAS_FUNCTION_RUNNER_GO124_ALPINE": "/bin/sh",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := env.Get("FAAS_FUNCTION_RUNNER_NODE22"); got != "/bin/sh" {
		t.Fatalf("Node22 = %q", got)
	}

	deleteValues := map[string]string{"FAAS_DATABASE_URL": "postgres://localhost/faas"}
	env, err = LoadFrom("imaged", mapLookup(deleteValues))
	if err == nil {
		t.Fatal("LoadFrom accepted missing required values")
	}
	var missing *MissingError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %T %v, want MissingError", err, err)
	}
	wantNames := []string{
		"FAAS_FUNCTION_RUNNER_GO124",
		"FAAS_FUNCTION_RUNNER_GO124_ALPINE",
		"FAAS_FUNCTION_RUNNER_NODE22",
		"FAAS_FUNCTION_RUNNER_NODE24",
		"FAAS_FUNCTION_RUNNER_PYTHON312",
		"FAAS_FUNCTION_RUNNER_PYTHON313",
	}
	if strings.Join(missing.Names, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("missing = %v, want %v", missing.Names, wantNames)
	}
	if missing.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2", missing.ExitCode())
	}
	if strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("missing error leaked an environment value: %v", err)
	}
	if len(env.Missing) != len(wantNames) {
		t.Fatalf("Env.Missing = %v", env.Missing)
	}
}

func TestLoadFromDatabaseURLAlias(t *testing.T) {
	env, err := LoadFrom("apid", mapLookup(map[string]string{
		"DATABASE_URL": "postgres:///faas?host=/run/postgresql",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := env.Get("FAAS_DATABASE_URL"); got == "" {
		t.Fatal("DATABASE_URL alias did not satisfy FAAS_DATABASE_URL")
	}
}

func TestLoadFromPathValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner")
	if err := os.WriteFile(path, []byte("runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"FAAS_DATABASE_URL":                 "postgres://localhost/faas",
		"FAAS_FUNCTION_RUNNER_NODE22":       path,
		"FAAS_FUNCTION_RUNNER_NODE24":       path,
		"FAAS_FUNCTION_RUNNER_PYTHON312":    path,
		"FAAS_FUNCTION_RUNNER_PYTHON313":    path,
		"FAAS_FUNCTION_RUNNER_GO124":        path,
		"FAAS_FUNCTION_RUNNER_GO124_ALPINE": path,
	}
	if _, err := LoadFrom("imaged", mapLookup(values)); err != nil {
		t.Fatalf("valid paths rejected: %v", err)
	}
	values["FAAS_FUNCTION_RUNNER_NODE22"] = filepath.Join(t.TempDir(), "missing")
	missingPath := values["FAAS_FUNCTION_RUNNER_NODE22"]
	if _, err := LoadFrom("imaged", mapLookup(values)); err == nil || !strings.Contains(err.Error(), "FAAS_FUNCTION_RUNNER_NODE22") {
		t.Fatalf("missing path error = %v", err)
	} else if strings.Contains(err.Error(), missingPath) {
		t.Fatalf("validation error leaked an environment value: %v", err)
	}
}

func TestLoadFromMalformedURLDoesNotLeakValue(t *testing.T) {
	secretURL := "postgres://user:password@[bad-host/faas"
	_, err := LoadFrom("apid", mapLookup(map[string]string{"FAAS_DATABASE_URL": secretURL}))
	if err == nil {
		t.Fatal("malformed URL accepted")
	}
	if strings.Contains(err.Error(), secretURL) || strings.Contains(err.Error(), "password") {
		t.Fatalf("validation error leaked an environment value: %v", err)
	}
}

func TestLoadFromUnknownDaemon(t *testing.T) {
	if _, err := LoadFrom("unknown", nil); err == nil {
		t.Fatal("unknown daemon accepted")
	}
}

func TestContractValidationKindsAreStable(t *testing.T) {
	if daemonunitspec.EnvValidationPathExists != "path-exists" ||
		daemonunitspec.EnvValidationSocket != "socket" ||
		daemonunitspec.EnvValidationURL != "url" ||
		daemonunitspec.EnvValidationInt != "int" {
		t.Fatal("validation kind changed")
	}
}
