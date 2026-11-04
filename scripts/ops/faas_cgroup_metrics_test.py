#!/usr/bin/env python3
"""Tests for the per-slice cgroup metrics collector (ADR-20261023124955)."""

import importlib.machinery
import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "deploy/ansible/roles/cgroup_metrics/files/faas-cgroup-metrics"
LOADER = importlib.machinery.SourceFileLoader("faas_cgroup_metrics", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
LOADER.exec_module(MODULE)


def make_cgroup(root, rel, files):
    path = root / rel
    path.mkdir(parents=True, exist_ok=True)
    (path / "cgroup.procs").write_text("")
    for name, body in files.items():
        (path / name).write_text(body)
    return path


FULL = {
    "cpu.stat": "usage_usec 2500000\nuser_usec 2000000\nsystem_usec 500000\nnr_periods 10\nnr_throttled 2\nthrottled_usec 125000\n",
    "memory.current": "1073741824\n",
    "memory.peak": "2147483648\n",
    "memory.max": "3758096384\n",
    "memory.high": "max\n",
    "memory.stat": "anon 805306368\nfile 268435456\nkernel 1000\n",
    "memory.events": "low 0\nhigh 4\nmax 1\noom 1\noom_kill 1\noom_group_kill 0\n",
    "io.stat": "8:0 rbytes=1000 wbytes=2000 rios=1 wios=2 dbytes=0 dios=0\n259:0 rbytes=500 wbytes=0 rios=1 wios=0 dbytes=0 dios=0\n",
    "cpu.pressure": "some avg10=1.00 avg60=0.50 avg300=0.10 total=3000000\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=1000000\n",
    "memory.pressure": "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
    "io.pressure": "some avg10=0.00 avg60=0.00 avg300=0.00 total=500000\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=250000\n",
}


class CgroupMetricsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name) / "cgroup"
        self.out = Path(self.temp.name) / "out" / "cgroups.prom"
        self.out.parent.mkdir()

    def tearDown(self):
        self.temp.cleanup()

    def run_collector(self, patterns):
        code = MODULE.main(
            env={
                "FAAS_CGROUP_METRICS_PATHS": patterns,
                "FAAS_CGROUP_ROOT": str(self.root),
                "FAAS_CGROUP_METRICS_TEXTFILE": str(self.out),
            },
            now=lambda: 1_700_000_000.0,
        )
        self.assertEqual(code, 0)
        return self.out.read_text()

    def test_full_cgroup_renders_every_family(self):
        make_cgroup(self.root, "faas-obs.slice", FULL)
        body = self.run_collector("faas-obs.slice")
        for line in (
            'faas_cgroup_cpu_usage_seconds_total{cgroup="faas-obs.slice"} 2.5',
            'faas_cgroup_cpu_throttled_seconds_total{cgroup="faas-obs.slice"} 0.125',
            'faas_cgroup_memory_bytes{cgroup="faas-obs.slice"} 1073741824',
            'faas_cgroup_memory_peak_bytes{cgroup="faas-obs.slice"} 2147483648',
            'faas_cgroup_memory_max_bytes{cgroup="faas-obs.slice"} 3758096384',
            'faas_cgroup_memory_anon_bytes{cgroup="faas-obs.slice"} 805306368',
            'faas_cgroup_memory_file_bytes{cgroup="faas-obs.slice"} 268435456',
            'faas_cgroup_memory_events_total{cgroup="faas-obs.slice",event="oom_kill"} 1',
            'faas_cgroup_io_bytes_total{cgroup="faas-obs.slice",direction="read"} 1500',
            'faas_cgroup_io_bytes_total{cgroup="faas-obs.slice",direction="write"} 2000',
            'faas_cgroup_pressure_stall_seconds_total{cgroup="faas-obs.slice",resource="cpu",kind="some"} 3',
            'faas_cgroup_pressure_stall_seconds_total{cgroup="faas-obs.slice",resource="io",kind="full"} 0.25',
            "faas_cgroup_metrics_last_run_timestamp_seconds 1700000000",
            "# TYPE faas_cgroup_cpu_usage_seconds_total counter",
        ):
            self.assertIn(line, body)
        # memory.high "max" means unlimited: no sample rather than a fake number.
        self.assertNotIn("faas_cgroup_memory_high_bytes{", body)

    def test_globs_match_children_and_skip_missing_paths(self):
        make_cgroup(self.root, "faas-cp.slice", {"cpu.stat": "usage_usec 1\n"})
        make_cgroup(self.root, "faas-cp.slice/faas-apid.service", {"cpu.stat": "usage_usec 2\n"})
        make_cgroup(self.root, "faas-cp.slice/faas-schedd.service", {"cpu.stat": "usage_usec 3\n"})
        body = self.run_collector("faas-cp.slice faas-cp.slice/*.service faas-obs.slice")
        self.assertIn('cgroup="faas-cp.slice/faas-apid.service"', body)
        self.assertIn('cgroup="faas-cp.slice/faas-schedd.service"', body)
        self.assertNotIn("faas-obs.slice", body)

    def test_systemd_nested_slices_keep_their_configured_names(self):
        # Production's layout (2026-10-24): systemd nests dashed slices under
        # their prefix, /faas.slice/faas-cp.slice/faas-apid.service.
        make_cgroup(self.root, "faas.slice/faas-cp.slice", {"cpu.stat": "usage_usec 1\n"})
        make_cgroup(self.root, "faas.slice/faas-cp.slice/faas-apid.service", {"cpu.stat": "usage_usec 2\n"})
        make_cgroup(self.root, "faas.slice/faas-cp.slice/faas-cp-build.slice", {"cpu.stat": "usage_usec 3\n"})
        make_cgroup(self.root, "faas.slice/faas-obs.slice/loki.service", {"cpu.stat": "usage_usec 4\n"})
        make_cgroup(self.root, "system.slice/system-postgresql.slice", {"cpu.stat": "usage_usec 5\n"})
        body = self.run_collector(
            "faas-cp.slice faas-cp.slice/*.service faas-cp-build.slice faas-obs.slice/*.service system.slice/system-postgresql.slice"
        )
        for label, value in (
            ("faas-cp.slice", "0.000001"),
            ("faas-cp.slice/faas-apid.service", "0.000002"),
            ("faas-cp-build.slice", "0.000003"),
            ("faas-obs.slice/loki.service", "0.000004"),
            ("system.slice/system-postgresql.slice", "0.000005"),
        ):
            self.assertIn(f'faas_cgroup_cpu_usage_seconds_total{{cgroup="{label}"}} {value}', body)
        self.assertNotIn('cgroup="faas.slice', body)

    def test_a_cgroup_listed_twice_is_reported_once(self):
        make_cgroup(self.root, "system.slice/system-postgresql.slice", {"cpu.stat": "usage_usec 7\n"})
        body = self.run_collector("system.slice/system-postgresql.slice system.slice/system-*.slice")
        self.assertEqual(body.count('cgroup="system.slice/system-postgresql.slice"'), 1)

    def test_plain_directories_are_not_cgroups(self):
        (self.root / "not-a-cgroup").mkdir(parents=True)
        body = self.run_collector("not-a-cgroup")
        self.assertNotIn("not-a-cgroup", body)
        self.assertIn("faas_cgroup_metrics_last_run_timestamp_seconds", body)

    def test_empty_pattern_list_is_an_error(self):
        self.assertEqual(MODULE.main(env={"FAAS_CGROUP_METRICS_PATHS": ""}), 2)


if __name__ == "__main__":
    unittest.main()
