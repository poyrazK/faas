"""Application startup compatibility checks for the Python preload."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class BootstrapTests(unittest.TestCase):
    def test_customer_sitecustomize_runs(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "sitecustomize.py").write_text('import os; os.environ["GREGALE_TEST_CUSTOMIZE"] = "ran"\n')
            env = dict(os.environ, FAAS_PROFILING_ENABLED="1", FAAS_PROFILING_ENDPOINT="http://127.0.0.1:1",
                       PYTHONPATH=os.pathsep.join([str(ROOT / "guest/profiling/python"), directory]))
            subprocess.run([sys.executable, "-c", 'import os; assert os.environ["GREGALE_TEST_CUSTOMIZE"] == "ran"'],
                           env=env, check=True, timeout=5)


if __name__ == "__main__":
    unittest.main()
