package templates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestPythonPostgresStarterManifest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "starter")
	if err := Materialize("rest-api-postgres-python", dest); err != nil {
		t.Fatal(err)
	}
	manifest, present, err := gregalemanifest.Load(dest)
	if err != nil || !present || manifest == nil {
		t.Fatalf("load Python manifest: %v, present=%v", err, present)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if manifest.Hosting == nil || manifest.Hosting.Health != "/healthz" || manifest.Release == nil || manifest.Release.Command != "python migrate.py" || manifest.Lifecycle == nil {
		t.Fatalf("missing database readiness/release/lifecycle: %+v", manifest)
	}
	hook := manifest.Lifecycle.ToAPI().AfterRestore
	if hook == nil || hook.Path != "/internal/after-restore" || hook.TimeoutMS != 2000 {
		t.Fatalf("restore hook = %#v", hook)
	}
	for _, name := range []string{"runtime/__init__.py", "runtime/_binding_client_runtime.py", "runtime/async_postgres_pool_runtime.py", "runtime/async_secret_reload_runtime.py", "runtime/postgres_pool_runtime.py", "runtime/secret_reload_runtime.py", ".gregaleignore"} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Fatalf("self-contained runtime missing %s: %v", name, err)
		}
	}
	file, err := os.ReadFile(filepath.Join(dest, "Dockerfile"))
	if err != nil || !strings.Contains(string(file), `com.gregale.secret-reload-signal="SIGHUP"`) ||
		!strings.Contains(string(file), `com.gregale.secret-reload-readiness="required"`) ||
		!strings.Contains(string(file), "COPY runtime ./runtime") || !strings.Contains(string(file), "USER 1000:1000") {
		t.Fatal("Python image must opt into atomic adoption and include the runtime as an unprivileged user", err)
	}
}

func runPythonPostgresStarter(t *testing.T, pattern string) {
	t.Helper()
	python := os.Getenv("GREGALE_PYTHON")
	explicit := python != ""
	if !explicit {
		python = "python3"
	}
	probe := exec.Command(python, "-I", "-c", "import fastapi, httpx, psycopg, psycopg_pool, uvicorn")
	if output, err := probe.CombinedOutput(); err != nil {
		if explicit {
			t.Fatalf("GREGALE_PYTHON lacks starter dependencies: %v\n%s", err, output)
		}
		t.Skip("install the Python starter requirements or select GREGALE_PYTHON to run lifecycle tests")
	}
	dest := filepath.Join(t.TempDir(), "starter")
	if err := Materialize("rest-api-postgres-python", dest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", "-m", "unittest", "discover", "-s", "tests", "-p", pattern, "-v")
	cmd.Dir = dest
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("materialized Python starter: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}

func TestPythonPostgresStarterLifecycle(t *testing.T) {
	runPythonPostgresStarter(t, "test_database.py")
}

func TestPythonPostgresStarterSQLRecovery(t *testing.T) {
	if os.Getenv("GREGALE_PYTHON_POSTGRES_TEST") != "1" {
		t.Skip("GREGALE_PYTHON_POSTGRES_TEST=1 and a disposable TLS/SCRAM DATABASE_URL required")
	}
	if os.Getenv("DATABASE_URL") == "" {
		t.Fatal("DATABASE_URL required")
	}
	runPythonPostgresStarter(t, "test_postgres.py")
}
