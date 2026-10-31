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
// A fourth, a uv project whose Procfile starts it with `uv run gunicorn`, runs
// on runner-python313 without uv (amendment 3).
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
	pyOwnBody   = "hello from faas (python own base)"
	py313Body   = "hello from faas (python313 shared)"
	py312Body   = "hello from faas (python312 shared)"
	pyUVRunBody = "hello from faas (uv run shared)"
)

// pythonApp is a WSGI app answering body.
func pythonApp(body string) string {
	return `def app(environ, start_response):
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [b"` + body + `\n"]
`
}

// pythonLayerFixture is a gunicorn app answering body. version, when set, is
// written to .python-version.
func pythonLayerFixture(t *testing.T, version, body string) []byte {
	t.Helper()
	files := map[string]string{
		"requirements.txt": "gunicorn==23.0.0\n",
		"app.py":           pythonApp(body),
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

// uvRunLock is uv 0.13.1's lock (Railpack 0.38 installs that uv) for
// uvRunFixture's pyproject.toml.
const uvRunLock = `version = 1
revision = 5
requires-python = ">=3.12"

[[package]]
name = "faas-uv-run"
version = "0.1.0"
source = { virtual = "." }
dependencies = [
    { name = "gunicorn" },
]

[package.metadata]
requires-dist = [{ name = "gunicorn", specifier = "==23.0.0" }]

[[package]]
name = "gunicorn"
version = "23.0.0"
source = { registry = "https://pypi.org/simple" }
dependencies = [
    { name = "packaging" },
]
sdist = { url = "https://files.pythonhosted.org/packages/34/72/9614c465dc206155d93eff0ca20d42e1e35afc533971379482de953521a4/gunicorn-23.0.0.tar.gz", hash = "sha256:f014447a0101dc57e294f6c18ca6b40227a4c90e9bdb586042628030cba004ec", size = 375031, upload-time = "2024-08-10T20:25:27.378Z" }
wheels = [
    { url = "https://files.pythonhosted.org/packages/cb/7d/6dac2a6e1eba33ee43f318edbed4ff29151a49b5d37f080aad1e6469bca4/gunicorn-23.0.0-py3-none-any.whl", hash = "sha256:ec400d38950de4dfd418cff8328b2c8faed0edb0d517d3394e457c317908ca4d", size = 85029, upload-time = "2024-08-10T20:25:24.996Z" },
]

[[package]]
name = "packaging"
version = "26.3"
source = { registry = "https://pypi.org/simple" }
sdist = { url = "https://files.pythonhosted.org/packages/7d/fa/3944b40b07da9ce895c0e6303a5ab7d53da063554f534556b134a54d6093/packaging-26.3.tar.gz", hash = "sha256:94edc256424af38762eb31306eed28beb9f0efc50a8837492c9d6fd6004aed79", size = 313412, upload-time = "2026-08-04T18:15:28.737Z" }
wheels = [
    { url = "https://files.pythonhosted.org/packages/63/34/ba1c580383c9eada3711951fef0795c80b829a078d72188184bcab9dd527/packaging-26.3-py3-none-any.whl", hash = "sha256:d7193f7c8e4e93f444fde0262bf90af30e16fa0ad0ad44cb553c87339b23cd1c", size = 129956, upload-time = "2026-08-04T18:15:27.159Z" },
]
`

// uvRunFixture is a uv project whose Procfile starts gunicorn with uv run.
func uvRunFixture(t *testing.T, body string) []byte {
	t.Helper()
	return buildTarGz(t, map[string]string{
		"pyproject.toml": `[project]
name = "faas-uv-run"
version = "0.1.0"
requires-python = ">=3.12"
dependencies = ["gunicorn==23.0.0"]
`,
		"uv.lock":          uvRunLock,
		"app.py":           pythonApp(body),
		"Procfile":         "web: uv run gunicorn --bind 0.0.0.0:${PORT:-3000} app:app\n",
		"faas-build-token": body + time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
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
	deployShared := func(t *testing.T, source []byte, body, idempotencyKey, wantBase string) state.Deployment {
		t.Helper()
		// Each runner base is staged by its first deployment.
		dep := app.deploy(t, source, idempotencyKey, sharedBaseStallWindow, sharedBaseCeiling)
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
		shared313 = deployShared(t, pythonLayerFixture(t, "", py313Body), py313Body, "bbbbbbbb-0000-4000-8000-000000000002", "python313")
		t.Logf("app layer bytes: own base=%d python313=%d", own.RootfsBytes, shared313.RootfsBytes)
	})
	t.Run("shared-python312", func(t *testing.T) {
		shared312 := deployShared(t, pythonLayerFixture(t, "3.12", py312Body), py312Body, "bbbbbbbb-0000-4000-8000-000000000003", "python312")
		t.Logf("app layer bytes: python312=%d", shared312.RootfsBytes)
	})
	t.Run("shared-python313-uv-run", func(t *testing.T) {
		// The layer limit holds only when imaged dropped `uv run`: the full
		// layer keeps mise's Python and uv.
		uvRun := deployShared(t, uvRunFixture(t, pyUVRunBody), pyUVRunBody, "bbbbbbbb-0000-4000-8000-000000000004", "python313")
		t.Logf("app layer bytes: uv run=%d", uvRun.RootfsBytes)
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
