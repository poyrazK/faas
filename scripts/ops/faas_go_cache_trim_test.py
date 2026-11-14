#!/usr/bin/env python3
import fcntl
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "deploy/ansible/roles/go_cache_retention/files/faas-go-cache-trim"
LOADER = importlib.machinery.SourceFileLoader("faas_go_cache_trim", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
LOADER.exec_module(MODULE)

NOW = 1_800_000_000.0
HOUR = 3600


class GoCacheTrimTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.lock = self.root / "lock" / "acceptance.lock"
        self.config = self.root / "policy.json"
        self.write_policy()

    def tearDown(self):
        self.temp.cleanup()

    def write_policy(self, **overrides):
        policy = {
            "filesystem": str(self.root),
            "high_water_percent": 85,
            "low_water_percent": 70,
            "lock_path": str(self.lock),
            "cache_globs": [str(self.root / "caches" / "*" / "go-build")],
            "age_steps_hours": [1, 48, 6],
        }
        policy.update(overrides)
        self.config.write_text(json.dumps(policy))

    def make_cache(self, name, readme=True):
        cache = self.root / "caches" / name / "go-build"
        cache.mkdir(parents=True)
        if readme:
            (cache / "README").write_text(MODULE.GO_CACHE_README + "\nSee 'go doc cmd/go'.\n")
        (cache / "trim.txt").write_text("0")
        return cache

    def entry(self, cache, digest, age_hours, kind="a"):
        shard = cache / digest[:2]
        shard.mkdir(exist_ok=True)
        path = shard / f"{digest * 1}-{kind}"
        path.write_bytes(b"x" * 100)
        mtime = NOW - age_hours * HOUR
        os.utime(path, (mtime, mtime))
        return path

    def sweep(self, usage):
        policy = MODULE.load_policy(str(self.config))
        with mock.patch.object(MODULE, "used_percent", side_effect=usage):
            return MODULE.sweep(policy, NOW)

    def test_below_high_water_touches_nothing(self):
        cache = self.make_cache("smoke")
        old = self.entry(cache, "aa" * 32, 500)
        result = self.sweep([50.0])
        self.assertEqual(result.outcome, "below_high_water")
        self.assertTrue(old.exists())

    def test_trims_oldest_steps_first_until_below_low_water(self):
        cache = self.make_cache("smoke")
        ancient = self.entry(cache, "aa" * 32, 100)
        day = self.entry(cache, "ab" * 32, 20)
        hour = self.entry(cache, "bb" * 32, 3, kind="d")
        fresh = self.entry(cache, "cc" * 32, 0.5)
        # before, after the 48 h step, after the 6 h step.
        result = self.sweep([90.0, 80.0, 60.0])
        self.assertEqual(result.outcome, "trimmed")
        self.assertFalse(ancient.exists())
        self.assertFalse(day.exists())
        # The 1 h step never ran: usage was already below the low-water mark.
        self.assertTrue(hour.exists())
        self.assertTrue(fresh.exists())
        self.assertEqual(result.removed_entries, 2)
        self.assertEqual(result.used_percent_after, 60.0)

    def test_still_full_after_every_step_reports_above_low_water(self):
        cache = self.make_cache("smoke")
        fresh = self.entry(cache, "cc" * 32, 0.5)
        stale = self.entry(cache, "dd" * 32, 2)
        result = self.sweep([95.0, 95.0, 95.0, 95.0])
        self.assertEqual(result.outcome, "above_low_water")
        self.assertFalse(stale.exists())
        # Never anything used within the last hour.
        self.assertTrue(fresh.exists())

    def test_skips_while_a_run_holds_the_lock(self):
        cache = self.make_cache("smoke")
        old = self.entry(cache, "aa" * 32, 500)
        self.lock.parent.mkdir(parents=True)
        with open(self.lock, "a") as held:
            fcntl.flock(held.fileno(), fcntl.LOCK_EX)
            result = self.sweep([99.0])
        self.assertEqual(result.outcome, "skipped_locked")
        self.assertTrue(old.exists())

    def test_never_touches_a_directory_without_the_go_readme(self):
        cache = self.make_cache("impostor", readme=False)
        old = self.entry(cache, "aa" * 32, 500)
        result = self.sweep([99.0, 99.0, 99.0, 99.0])
        self.assertEqual(result.caches, 0)
        self.assertTrue(old.exists())

    def test_only_removes_go_entry_names(self):
        cache = self.make_cache("smoke")
        stray = cache / "aa" / "notes.txt"
        (cache / "aa").mkdir()
        stray.write_text("keep")
        other = cache / "zz"
        other.mkdir()
        odd = other / ("ee" * 32 + "-a")
        odd.write_text("keep")
        for path in (stray, odd):
            os.utime(path, (NOW - 500 * HOUR,) * 2)
        self.sweep([99.0, 50.0])
        self.assertTrue(stray.exists())
        self.assertTrue(odd.exists())
        self.assertTrue((cache / "README").exists())
        self.assertTrue((cache / "trim.txt").exists())

    def test_ignores_symlinked_caches(self):
        real = self.make_cache("real")
        old = self.entry(real, "aa" * 32, 500)
        link_parent = self.root / "caches" / "link"
        link_parent.mkdir()
        (link_parent / "go-build").symlink_to(real)
        policy = MODULE.load_policy(str(self.config))
        caches = MODULE.find_caches(policy)
        self.assertEqual(caches, [real])
        self.assertTrue(old.exists())

    def test_policy_validation(self):
        cases = [
            {"low_water_percent": 90},
            {"high_water_percent": 101},
            {"age_steps_hours": []},
            {"age_steps_hours": [0.5]},
            {"cache_globs": ["relative/go-build"]},
        ]
        for override in cases:
            with self.subTest(override=override):
                self.write_policy(**override)
                with self.assertRaises(MODULE.PolicyError):
                    MODULE.load_policy(str(self.config))

    def test_metrics_mark_the_current_outcome(self):
        result = MODULE.Result(used_percent_before=90, used_percent_after=60, freed_bytes=4096,
                               removed_entries=3, caches=1, outcome="trimmed")
        text = MODULE.render_metrics(result, NOW)
        self.assertIn('faas_go_cache_trim_outcome{outcome="trimmed"} 1', text)
        self.assertIn('faas_go_cache_trim_outcome{outcome="skipped_locked"} 0', text)
        self.assertIn("faas_go_cache_trim_freed_bytes 4096", text)

    def test_defaults_match_the_native_runners(self):
        defaults = (ROOT / "deploy/ansible/roles/go_cache_retention/defaults/main.yml").read_text()
        self.assertIn("/var/lock/faas-builder-acceptance.lock", defaults)
        for runner in ("run-native-metal-smoke.sh", "run-native-e2e.sh", "run-native-builder-acceptance.sh"):
            text = (ROOT / "scripts/ci" / runner).read_text()
            self.assertIn("exec 9>/var/lock/faas-builder-acceptance.lock", text, runner)
            self.assertRegex(text, r'cache_root="/var/cache/faas-[a-z0-9-]+"', runner)


if __name__ == "__main__":
    unittest.main()
