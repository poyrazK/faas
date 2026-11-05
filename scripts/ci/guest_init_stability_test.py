#!/usr/bin/env python3
"""Unit tests for guest_init_stability.py (an unreferenced file must not change
guest-init's code)."""
import importlib.util
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
REPO = HERE.parent.parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / f"{name}.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


gis = load("guest_init_stability")
gir = gis.gir
girt = load("guest_init_reuse_test")


class RecipeTest(unittest.TestCase):
    def test_flags_and_tags_come_from_the_makefile(self):
        makefile = "GO_BUILD_FLAGS := -trimpath -buildvcs=false\nGUEST_TAGS := grpcnotrace\n"
        flags, tags = gis.go_build_flags(makefile), gis.cdd.guest_tags(makefile)
        self.assertEqual(flags, ["-trimpath", "-buildvcs=false"])
        self.assertEqual(gis.build_command(flags, tags, "out"),
                         ["go", "build", "-trimpath", "-buildvcs=false", "-tags=grpcnotrace", "-o", "out", "./guest/init"])
        self.assertEqual(gis.build_command(flags, [], "out")[-3:], ["-o", "out", "./guest/init"])
        with self.assertRaisesRegex(ValueError, "GO_BUILD_FLAGS"):
            gis.go_build_flags("A := 1\n")

    def test_the_repository_recipe_is_the_release_recipe(self):
        # The check must build guest-init the way the release does.
        makefile = (REPO / "Makefile").read_text(encoding="utf-8")
        builds = [line for line in makefile.split("\n") if "-o $(BINDIR)/init" in line]
        self.assertEqual(len(builds), 2, "make build and make build-release-batch each build init")
        for line in builds:
            self.assertIn("$(GO_BUILD_FLAGS) -tags $(GUEST_TAGS) -o $(BINDIR)/init", line)
        self.assertEqual(gis.go_build_flags(makefile), ["-trimpath", "-buildvcs=false"])
        self.assertEqual(gis.cdd.guest_tags(makefile), ["grpcnotrace"])


class CheckTest(unittest.TestCase):
    def fake_builder(self, probe_text):
        """A builder that writes a guest-init-shaped ELF whose code depends on
        whether the probe file exists, as `probe_text` says."""
        def builder(tree, out, flags, tags):
            probe_exists = os.path.exists(os.path.join(tree, gis.PROBE_PATH))
            text = probe_text if probe_exists else b"\x90" * 16
            with open(out, "wb") as fh:
                fh.write(girt.guest_init(text=text, build_id=b"probe" if probe_exists else b"base"))
        return builder

    def run_check(self, probe_text):
        with tempfile.TemporaryDirectory() as tree:
            os.makedirs(os.path.join(tree, "pkg", "api"))
            verdict = gis.check(tree, ["-trimpath"], ["grpcnotrace"], self.fake_builder(probe_text))
            with open(os.path.join(tree, gis.PROBE_PATH), encoding="utf-8") as fh:
                self.assertEqual(fh.read(), gis.PROBE)
        return verdict

    def test_an_unreferenced_file_that_leaves_the_code_alone_passes(self):
        # The build ID differs between the two builds, as it does for real.
        self.assertEqual(self.run_check(b"\x90" * 16), gir.LINES)

    def test_an_unreferenced_file_that_changes_the_code_fails(self):
        self.assertEqual(self.run_check(b"\xcc" * 16), gir.CHANGED)


@unittest.skipUnless(shutil.which("go"), "go is not installed")
class ProbeCompilesTest(unittest.TestCase):
    def test_the_probe_is_valid_go_for_package_api(self):
        # gofmt -e parses without type checking: enough to keep the probe from
        # rotting into a syntax error that every nightly run would report as
        # a build failure.
        goroot = subprocess.run(["go", "env", "GOROOT"], capture_output=True, text=True, check=True).stdout.strip()
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "probe.go")
            with open(path, "w", encoding="utf-8") as fh:
                fh.write(gis.PROBE)
            run = subprocess.run([os.path.join(goroot, "bin", "gofmt"), "-e", "-l", path],
                                 capture_output=True, text=True)
            self.assertEqual(run.returncode, 0, run.stderr)
            self.assertEqual(run.stdout.strip(), "", "the probe is not gofmt-clean")
        pkg_line = next(line for line in (REPO / "pkg/api/limits.go").read_text().split("\n")
                        if line.startswith("package "))
        self.assertEqual(gis.PROBE.split("\n")[0], pkg_line)


if __name__ == "__main__":
    unittest.main()
