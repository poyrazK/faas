//go:build metal

// shared_runtime_python_metal_test.go — ADR-1500 amendment 2 acceptance:
// Python source apps on the shared runner bases.
//
// One app gets three deployments of a gunicorn app, started through the
// virtualenv's console script so the relinked interpreter is what runs:
//   - own base: Python 3.11 has no shared runtime and keeps its toolchain in
//     drive1 above base-minimal;
//   - python313: no declaration, an /app-only drive1 on runner-python313
//     (Wolfi, /usr/bin/python3.13);
//   - python312: .python-version 3.12, an /app-only drive1 on runner-python312
//     (Debian, /usr/local/bin/python3.12).
//
// Each serves when deployed and after a snapshot restore, and the own-base and
// python313 deployments each serve after a rollback onto them, from a cold
// boot and from a snapshot restore.
package e2e_test

import (
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	pyOwnBody = "hello from faas (python own base)"
	py313Body = "hello from faas (python313 shared)"
	py312Body = "hello from faas (python312 shared)"
)

// pythonLayerFixture is a gunicorn app answering body. version, when set, is
// written to .python-version.
func pythonLayerFixture(t *testing.T, version, body string) []byte {
	t.Helper()
	files := map[string]string{
		"requirements.txt": "gunicorn==23.0.0\n",
		"app.py": `def app(environ, start_response):
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [b"` + body + `\n"]
`,
		// Railpack starts a requirements.txt app with python; the console
		// script exercises the virtualenv's interpreter link instead.
		"railpack.json":    `{"deploy": {"startCommand": "gunicorn --bind 0.0.0.0:${PORT:-3000} app:app"}}` + "\n",
		"faas-build-token": body + time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	if version != "" {
		files[".python-version"] = version + "\n"
	}
	return buildTarGz(t, files)
}

func TestSharedRuntimePythonMetal(t *testing.T) {
	app := newSharedRuntimeApp(t, "pyshared", py313Body)
	if app == nil {
		return
	}
	guestInit, err := os.Stat(os.Getenv("FAAS_GUEST_INIT"))
	if err != nil {
		t.Fatalf("stat FAAS_GUEST_INIT: %v", err)
	}
	// Every app layer carries guest-init. Beyond it a shared layer holds the
	// fixture and its virtualenv (pip and gunicorn, about 15 MB), while
	// Railpack's mise Python alone is over 100 MB.
	sharedLimit := guestInit.Size() + 48<<20
	deployShared := func(t *testing.T, version, body, idempotencyKey, wantBase string) state.Deployment {
		t.Helper()
		// Each runner base is staged by its first deployment.
		dep := app.deploy(t, pythonLayerFixture(t, version, body), idempotencyKey, sharedBaseStallWindow, sharedBaseCeiling)
		if dep.RuntimeBase != wantBase {
			t.Fatalf("deployment runtime_base = %q, want %s", dep.RuntimeBase, wantBase)
		}
		if dep.RootfsBytes <= 0 || dep.RootfsBytes > sharedLimit {
			t.Fatalf("app layer = %d bytes, want guest-init (%d) plus /app, at most %d", dep.RootfsBytes, guestInit.Size(), sharedLimit)
		}
		app.park(t)
		app.wake(t, body, "restore")
		app.park(t)
		return dep
	}

	var own, shared313 state.Deployment
	t.Run("own-base-python311", func(t *testing.T) {
		own = app.deploy(t, pythonLayerFixture(t, "3.11", pyOwnBody), "bbbbbbbb-0000-4000-8000-000000000001", sharedBaseStallWindow, sharedBaseCeiling)
		if own.RuntimeBase != "" {
			t.Fatalf("Python 3.11 deployment runtime_base = %q, want the app's own base", own.RuntimeBase)
		}
		app.park(t)
		app.wake(t, pyOwnBody, "restore")
		app.park(t)
	})
	t.Run("shared-python313", func(t *testing.T) {
		shared313 = deployShared(t, "", py313Body, "bbbbbbbb-0000-4000-8000-000000000002", "python313")
		t.Logf("app layer bytes: own base=%d python313=%d", own.RootfsBytes, shared313.RootfsBytes)
	})
	t.Run("shared-python312", func(t *testing.T) {
		shared312 := deployShared(t, "3.12", py312Body, "bbbbbbbb-0000-4000-8000-000000000003", "python312")
		t.Logf("app layer bytes: python312=%d", shared312.RootfsBytes)
	})
	t.Run("rollback-to-own-base", func(t *testing.T) {
		if own.ID == "" || shared313.ID == "" {
			t.Skip("an earlier deployment did not go live")
		}
		app.rollback(t, own)
		app.park(t)
		app.coldThenRestore(t, own, pyOwnBody)
	})
	t.Run("rollback-to-shared-python313", func(t *testing.T) {
		if own.ID == "" || shared313.ID == "" {
			t.Skip("an earlier deployment did not go live")
		}
		app.rollback(t, shared313)
		app.park(t)
		app.coldThenRestore(t, shared313, py313Body)
	})
}
