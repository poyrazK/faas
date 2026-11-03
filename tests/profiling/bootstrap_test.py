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


class HeapGuardTests(unittest.TestCase):
    """A heap capture must never cause the OOM kill it explains (ADR-1155)."""

    def setUp(self):
        sys.path.insert(0, str(ROOT / "guest/profiling/python"))
        import _gregale_heap
        self.heap = _gregale_heap

    def tearDown(self):
        sys.path.remove(str(ROOT / "guest/profiling/python"))
        self.heap.stop()

    def test_tracing_stops_below_the_floor(self):
        mib = 1 << 20
        self.assertTrue(self.heap.low_memory(available=10 * mib, total=512 * mib))   # under 16 MiB
        self.assertTrue(self.heap.low_memory(available=100 * mib, total=4096 * mib))  # under 5%
        self.assertFalse(self.heap.low_memory(available=100 * mib, total=512 * mib))

    def test_snapshot_needs_room_for_its_copy(self):
        mib = 1 << 20
        self.assertFalse(self.heap.snapshot_affordable(available=50 * mib, traced=10 * mib))
        self.assertTrue(self.heap.snapshot_affordable(available=100 * mib, traced=10 * mib))

    def test_unaffordable_snapshot_is_skipped_and_stops_tracing(self):
        self.heap.start(self.heap.LOW_MEMORY_FRAMES)
        keep = [bytearray(1024) for _ in range(100)]
        original = self.heap.snapshot_affordable
        self.heap.snapshot_affordable = lambda *args, **kwargs: False
        try:
            self.assertIsNone(self.heap.snapshot_pprof(0, stop=True))
        finally:
            self.heap.snapshot_affordable = original
        import tracemalloc
        self.assertFalse(tracemalloc.is_tracing())
        self.assertEqual(len(keep), 100)

    def test_memory_reads_this_host(self):
        available, total = self.heap.memory()
        if available is not None:
            self.assertGreater(total, 0)
            self.assertLessEqual(available, total)


if __name__ == "__main__":
    unittest.main()
