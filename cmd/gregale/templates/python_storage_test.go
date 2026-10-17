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

func TestPythonStorageStarterManifest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "starter")
	if err := Materialize("object-storage-python", dest); err != nil {
		t.Fatal(err)
	}
	manifest, present, err := gregalemanifest.Load(dest)
	if err != nil || !present || manifest == nil {
		t.Fatalf("load storage manifest: %v, present=%v", err, present)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if manifest.Hosting == nil || manifest.Hosting.Health != "/readyz" || manifest.Release != nil || manifest.Lifecycle == nil {
		t.Fatalf("missing storage readiness/lifecycle: %+v", manifest)
	}
	hook := manifest.Lifecycle.ToAPI().AfterRestore
	if hook == nil || hook.Path != "/internal/after-restore" || hook.TimeoutMS != 2000 {
		t.Fatalf("restore hook = %#v", hook)
	}
	for _, name := range []string{"runtime/__init__.py", "runtime/_binding_client_runtime.py", "runtime/_async_binding_client_runtime.py", "runtime/async_object_storage_runtime.py", "runtime/async_secret_reload_runtime.py", "runtime/object_storage_runtime.py", "runtime/secret_reload_runtime.py", "tests/test_https.py", "tests/tls_fixture.py", ".gregaleignore", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Fatalf("self-contained runtime missing %s: %v", name, err)
		}
	}
	file, err := os.ReadFile(filepath.Join(dest, "Dockerfile"))
	if err != nil || !strings.Contains(string(file), `com.gregale.secret-reload-signal="SIGHUP"`) ||
		!strings.Contains(string(file), `com.gregale.secret-reload-readiness="required"`) ||
		!strings.Contains(string(file), "COPY runtime ./runtime") || !strings.Contains(string(file), "USER 1000:1000") {
		t.Fatal("Python image must include the runtime and opt into adoption as an unprivileged user", err)
	}
}

func TestPythonStorageStarterAcceptance(t *testing.T) {
	python := os.Getenv("GREGALE_PYTHON")
	explicit := python != ""
	if !explicit {
		python = "python3"
	}
	probe := exec.Command(python, "-I", "-c", "import aiobotocore, botocore, fastapi, httpx, uvicorn")
	if output, err := probe.CombinedOutput(); err != nil {
		if explicit {
			t.Fatalf("GREGALE_PYTHON lacks storage starter dependencies: %v\n%s", err, output)
		}
		t.Skip("install Python storage starter requirements or select GREGALE_PYTHON")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Fatal("OpenSSL is required for Python storage HTTPS acceptance")
	}
	dest := filepath.Join(t.TempDir(), "starter")
	if err := Materialize("object-storage-python", dest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", "-m", "unittest", "discover", "-s", "tests", "-v")
	cmd.Dir = dest
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("materialized Python storage starter: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
