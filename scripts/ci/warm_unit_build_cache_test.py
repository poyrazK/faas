#!/usr/bin/env python3
"""Unit tests for warm_unit_build_cache.py (release-units' build cache)."""
import importlib.util
import json
import pathlib
import re
import tempfile
import unittest
from unittest import mock

HERE = pathlib.Path(__file__).parent
REPO = HERE.parent.parent
spec = importlib.util.spec_from_file_location("warm_unit_build_cache", HERE / "warm_unit_build_cache.py")
warm = importlib.util.module_from_spec(spec)
spec.loader.exec_module(warm)

MAKEFILE = """\
GO ?= go
DAEMONS := apid gatewayd-internal vmmd
CLIS := gregale
GO_BUILD_FLAGS := -trimpath -buildvcs=false
DAEMON_TAGS := metal,grpcnotrace
"""

CONFIG = {"binaries": {
    "apid": {"lane": "control-plane"},
    "gatewayd-internal": {"lane": "edge"},
    "vmmd": {"lane": "compute"},
    "schedd": {"lane": "shared"},
    "gregale": {"lane": "tool"},
}}


class LaneBinariesTest(unittest.TestCase):
    def test_only_deployable_lanes(self):
        self.assertEqual(warm.lane_binaries(CONFIG), ["apid", "gatewayd-internal"])

    def test_real_config_covers_both_lanes(self):
        config = json.loads((HERE / "release_units.json").read_text(encoding="utf-8"))
        lanes = warm.lane_binaries(config)
        self.assertIn("apid", lanes)
        self.assertIn("gatewayd-internal", lanes)
        self.assertIn("gatewayd-public", lanes)
        for other in ("schedd", "vmmd", "gregale"):
            self.assertNotIn(other, lanes)


class ToolsTest(unittest.TestCase):
    def test_tools_are_what_the_release_build_carries(self):
        script = (REPO / "scripts" / "build-unit-release.sh").read_text(encoding="utf-8")
        carried = set(re.findall(r"-o \"\$work/tools/[a-z]+\" \./cmd/([a-z]+)", script))
        self.assertEqual(carried, set(warm.TOOLS))


class WarmTest(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        pathlib.Path(self.root, "Makefile").write_text(MAKEFILE, encoding="utf-8")
        pathlib.Path(self.root, "go.mod").write_text("module example.com/faas\n\ngo 1.26\n", encoding="utf-8")
        self.config = pathlib.Path(self.root, "release_units.json")
        self.config.write_text(json.dumps(CONFIG), encoding="utf-8")

    def test_builds_lanes_with_the_release_recipe_and_the_tools_without_tags(self):
        closures = mock.Mock(return_value={})
        built = mock.Mock(return_value={})
        run = mock.Mock(return_value=mock.Mock(returncode=0, stderr=""))
        with mock.patch.object(warm.affected_units, "binary_closures", closures), \
                mock.patch.object(warm.affected_units, "git", return_value="f" * 40 + "\n"), \
                mock.patch.object(warm.binary_code, "build", built), \
                mock.patch.object(warm.subprocess, "run", run):
            out = warm.warm(self.root, str(self.config))

        self.assertEqual(out, ["apid", "gatewayd-internal", "deployctl", "migrate", "gregalectl"])
        daemons, clis, module, root, tags = closures.call_args.args
        self.assertEqual((daemons, clis, module, tags), (["apid", "gatewayd-internal", "vmmd"], ["gregale"],
                                                         "example.com/faas", ["metal", "grpcnotrace"]))
        side, names, _ = built.call_args.args
        self.assertEqual(names, ["apid", "gatewayd-internal"])
        self.assertEqual(side.flags, ["-trimpath", "-buildvcs=false"])
        self.assertEqual(side.tags("apid"), ["metal", "grpcnotrace"])

        cmd = run.call_args.args[0]
        env = run.call_args.kwargs["env"]
        self.assertEqual(cmd[:4], ["go", "build", "-trimpath", "-buildvcs=false"])
        self.assertEqual(cmd[-3:], ["./cmd/deployctl", "./cmd/migrate", "./cmd/gregalectl"])
        self.assertFalse(any(a.startswith("-tags") for a in cmd))
        self.assertEqual((env["GOOS"], env["GOARCH"], env["CGO_ENABLED"]), ("linux", "amd64", "0"))

    def test_a_failed_tool_build_fails(self):
        with mock.patch.object(warm.affected_units, "binary_closures", return_value={}), \
                mock.patch.object(warm.affected_units, "git", return_value="f" * 40 + "\n"), \
                mock.patch.object(warm.binary_code, "build", return_value={}), \
                mock.patch.object(warm.subprocess, "run",
                                  return_value=mock.Mock(returncode=1, stderr="cannot find package")):
            with self.assertRaisesRegex(RuntimeError, "cannot find package"):
                warm.warm(self.root, str(self.config))


if __name__ == "__main__":
    unittest.main()
