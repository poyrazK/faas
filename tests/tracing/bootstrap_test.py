"""Application startup compatibility checks for the tracing preloads (ADR-958)."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
TRACING = ROOT / "guest/tracing/python"
PROFILING = ROOT / "guest/profiling/python"


_VENV = None


def setUpModule():
    # A venv without system site-packages keeps the checks hermetic: a host
    # interpreter that already ships opentelemetry-sdk would otherwise make
    # the bootstrap (correctly) treat every process as self-instrumented.
    global _VENV
    _VENV = tempfile.TemporaryDirectory()
    subprocess.run([sys.executable, "-m", "venv", "--without-pip", _VENV.name], check=True, timeout=60)


def tearDownModule():
    _VENV.cleanup()


def run_python(code, **env):
    full = dict(os.environ, **env)
    full.pop("PYTHONHOME", None)
    python = str(Path(_VENV.name, "bin", "python"))
    return subprocess.run([python, "-c", code], env=full, check=True, timeout=10,
                          capture_output=True, text=True)


class PythonBootstrapTests(unittest.TestCase):
    def test_customer_sitecustomize_runs(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "sitecustomize.py").write_text('import os; os.environ["GREGALE_TEST_CUSTOMIZE"] = "ran"\n')
            run_python('import os; assert os.environ["GREGALE_TEST_CUSTOMIZE"] == "ran"',
                       FAAS_TRACING_ENABLED="1", PYTHONPATH=os.pathsep.join([str(TRACING), directory]))

    def test_chains_through_profiling_bootstrap(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "sitecustomize.py").write_text('import os; os.environ["GREGALE_TEST_CUSTOMIZE"] = "ran"\n')
            run_python('import os; assert os.environ["GREGALE_TEST_CUSTOMIZE"] == "ran"',
                       FAAS_TRACING_ENABLED="1",
                       PYTHONPATH=os.pathsep.join([str(TRACING), str(PROFILING), directory]))

    def test_full_platform_stack_runs_customer_once(self):
        # guest-init stamps reseed, then tracing, then profiling ahead of the
        # app. Each must chain forward exactly once without recursing.
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "sitecustomize.py").write_text(
                'import os; os.environ["GREGALE_TEST_RUNS"] = os.environ.get("GREGALE_TEST_RUNS", "") + "x"\n')
            out = run_python('import os; print(os.environ.get("GREGALE_TEST_RUNS"))',
                             FAAS_TRACING_ENABLED="1", FAAS_PROFILING_ENABLED="1",
                             FAAS_PROFILING_ENDPOINT="http://127.0.0.1:1",
                             PYTHONPATH=os.pathsep.join([str(ROOT / "guest/init/rngpreload/python"), str(TRACING),
                                                         str(PROFILING), directory]))
            self.assertEqual(out.stdout.strip(), "x")
            self.assertNotIn("Error in sitecustomize", out.stderr)

    def test_missing_packages_never_break_startup(self):
        # Without lib/ installed the import fails; the app must still start.
        out = run_python('print("served")', FAAS_TRACING_ENABLED="1", PYTHONPATH=str(TRACING))
        self.assertEqual(out.stdout.strip(), "served")

    def _boot_copy(self, directory):
        boot = Path(directory, "python")
        shutil.copytree(TRACING, boot, ignore=shutil.ignore_patterns("requirements.*"))
        Path(boot, "lib").mkdir()
        Path(boot, "deps").mkdir()
        return boot

    def test_otel_lib_prepended_and_deps_appended(self):
        # OpenTelemetry must come from one coherent version set (lib first);
        # shared third-party dependencies must defer to the app (deps last).
        with tempfile.TemporaryDirectory() as directory:
            boot = self._boot_copy(directory)
            lib, deps = str(Path(boot, "lib").resolve()), str(Path(boot, "deps").resolve())
            # The interpreter puts the script directory ('' for -c) ahead of site
            # initialisation, so lib must lead every other entry.
            run_python(f'import sys; entries = [p for p in sys.path if p]; assert entries[0] == {lib!r} and entries[-1] == {deps!r}, sys.path',
                       FAAS_TRACING_ENABLED="1", PYTHONPATH=str(boot))

    def test_app_owned_sdk_is_left_alone(self):
        with tempfile.TemporaryDirectory() as directory:
            boot = self._boot_copy(directory)
            app = Path(directory, "app")
            Path(app, "opentelemetry", "sdk").mkdir(parents=True)
            Path(app, "opentelemetry", "sdk", "__init__.py").write_text("")
            lib = str(Path(boot, "lib").resolve())
            run_python(f'import sys; assert {lib!r} not in sys.path, sys.path',
                       FAAS_TRACING_ENABLED="1", PYTHONPATH=os.pathsep.join([str(boot), str(app)]))

    def test_disabled_does_not_touch_sys_path(self):
        with tempfile.TemporaryDirectory() as directory:
            boot = self._boot_copy(directory)
            lib = str(Path(boot, "lib").resolve())
            run_python(f'import sys; assert {lib!r} not in sys.path', PYTHONPATH=str(boot))


class NodeBootstrapTests(unittest.TestCase):
    @unittest.skipIf(shutil.which("node") is None, "node not installed")
    def test_missing_packages_never_break_startup(self):
        out = subprocess.run(["node", "--require", str(ROOT / "guest/tracing/node.cjs"), "-e", 'console.log("served")'],
                             env=dict(os.environ, FAAS_TRACING_ENABLED="1"), check=True, timeout=10,
                             capture_output=True, text=True)
        self.assertEqual(out.stdout.strip(), "served")

    @unittest.skipIf(shutil.which("node") is None, "node not installed")
    def test_sdk_start_keeps_default_sigterm_exit(self):
        # guest-init stops apps with SIGTERM; the bootstrap must not install
        # a SIGTERM listener, which would suppress Node's default exit.
        with tempfile.TemporaryDirectory() as directory:
            shutil.copy(ROOT / "guest/tracing/node.cjs", Path(directory, "node.cjs"))
            stubs = {
                "@opentelemetry/sdk-node": "exports.NodeSDK = class { start() { process.stdout.write('started\\n'); } shutdown() { return Promise.resolve(); } };",
                "@opentelemetry/auto-instrumentations-node": "exports.getNodeAutoInstrumentations = () => [];",
                "@opentelemetry/exporter-trace-otlp-proto": "exports.OTLPTraceExporter = class {};",
            }
            for name, source in stubs.items():
                pkg = Path(directory, "node_modules", name)
                pkg.mkdir(parents=True)
                Path(pkg, "index.js").write_text(source)
            app = subprocess.Popen(["node", "--require", str(Path(directory, "node.cjs")), "-e",
                                    "setInterval(() => {}, 1000); process.stdout.write('ready\\n');"],
                                   env=dict(os.environ, FAAS_TRACING_ENABLED="1"), stdout=subprocess.PIPE, text=True)
            try:
                self.assertEqual(app.stdout.readline().strip(), "started")
                self.assertEqual(app.stdout.readline().strip(), "ready")
                app.terminate()
                self.assertEqual(app.wait(timeout=5), -15)
            finally:
                if app.poll() is None:
                    app.kill()
                app.stdout.close()

    @unittest.skipIf(shutil.which("node") is None, "node not installed")
    def test_disabled_is_inert(self):
        out = subprocess.run(["node", "--require", str(ROOT / "guest/tracing/node.cjs"), "-e",
                              'console.log(Object.keys(require.cache).some(k => k.includes("@opentelemetry")))'],
                             env={k: v for k, v in os.environ.items() if k != "FAAS_TRACING_ENABLED"},
                             check=True, timeout=10, capture_output=True, text=True)
        self.assertEqual(out.stdout.strip(), "false")


if __name__ == "__main__":
    unittest.main()
