#!/usr/bin/env python3
"""Unit tests for blast_radius.py (release binaries a change rebuilds)."""
import importlib.util
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
REPO = HERE.parent.parent
# Throwaway repositories must not start git's detached auto-maintenance
# (affected_units_test.py explains why).
GIT = ("git", "-c", "maintenance.auto=false", "-c", "gc.auto=0")
spec = importlib.util.spec_from_file_location("blast_radius", HERE / "blast_radius.py")
br = importlib.util.module_from_spec(spec)
spec.loader.exec_module(br)
au = br.au


class FakeTree:
    """A Tree without a checkout: closures are (dirs, files) per binary."""

    def __init__(self, closures, files, recipe="recipe", daemons=(), sha="b" * 40):
        self.closures, self.files, self.recipe, self.daemons = closures, set(files), recipe, list(daemons)
        self.sha = sha


APID = ({"cmd/apid", "pkg/api"}, {"cmd/apid/main.go", "pkg/api/api.go"})
VMMD = ({"cmd/vmmd", "pkg/fc"}, {"cmd/vmmd/main.go", "pkg/fc/fc.go"})
FILES = APID[1] | VMMD[1] | {"go.mod", "Makefile", "README.md"}


class CandidatesTest(unittest.TestCase):
    def test_cases(self):
        cases = [
            ("a file in one closure selects that binary",
             ["pkg/api/api.go"], {}, {}, {"apid": ["pkg/api/api.go"]}),
            ("a file outside every closure selects nothing",
             ["README.md", "docs/x.md"], {}, {}, {}),
            ("test files are never inputs",
             ["pkg/api/api_test.go"], {}, {}, {}),
            ("a global input selects every binary",
             ["go.mod"], {}, {}, {"apid": ["go.mod"], "vmmd": ["go.mod"]}),
            ("a file deleted from a closure package selects the binary",
             ["pkg/fc/gone.go"], {}, {}, {"vmmd": ["pkg/fc/gone.go"]}),
            # vmmd stopped importing pkg/api: the file is in its base closure only.
            ("a package dropped from a closure selects the binary",
             ["cmd/vmmd/main.go", "pkg/api/api.go"],
             {"vmmd": (VMMD[0] | {"pkg/api"}, VMMD[1] | {"pkg/api/api.go"})}, {},
             {"apid": ["pkg/api/api.go"], "vmmd": ["cmd/vmmd/main.go", "pkg/api/api.go"]}),
            ("a binary on one side only is added or removed",
             [], {"old": APID}, {"new": APID}, {"new": ["added"], "old": ["removed"]}),
        ]
        for name, paths, base_extra, head_extra, want in cases:
            with self.subTest(name):
                base = FakeTree({"apid": APID, "vmmd": VMMD, **base_extra}, FILES)
                head = FakeTree({"apid": APID, "vmmd": VMMD, **head_extra}, FILES)
                if "old" in base_extra:
                    del head.closures["vmmd"], base.closures["vmmd"]
                    del head.closures["apid"], base.closures["apid"]
                self.assertEqual(br.candidates(paths, base, head, {"go.mod", "go.sum"}), want)

    def test_build_recipe_change_selects_every_binary(self):
        base = FakeTree({"apid": APID, "vmmd": VMMD}, FILES, recipe="old")
        head = FakeTree({"apid": APID, "vmmd": VMMD}, FILES, recipe="new")
        got = br.candidates(["Makefile"], base, head, set())
        self.assertEqual(got, {"apid": ["Makefile (release build recipe)"],
                               "vmmd": ["Makefile (release build recipe)"]})


class DaemonTagsTest(unittest.TestCase):
    def test_daemon_tags(self):
        self.assertEqual(br.daemon_tags("DAEMONS := a b\n"), ["metal"])
        self.assertEqual(br.daemon_tags("DAEMON_TAGS := metal,grpcnotrace\n"), ["metal", "grpcnotrace"])

    def test_repository_daemon_tags_are_what_the_recipe_uses(self):
        makefile = (REPO / "Makefile").read_text(encoding="utf-8")
        tags = br.daemon_tags(makefile)
        self.assertIn("metal", tags)
        if "DAEMON_TAGS :=" in makefile:
            self.assertNotIn("-tags metal", au.build_recipe(makefile))


class CompareTest(unittest.TestCase):
    def test_compare(self):
        base = {"a": ("f1", "c1"), "b": ("f2", "c2"), "l": ("f3", "c3"), "gone": ("f4", "c4")}
        head = {"a": ("f1", "c1"), "b": ("f9", "c9"), "l": ("f8", "c3"), "new": ("f5", "c5")}
        got = br.compare(base, head, ["a", "b", "l", "gone", "new"])
        self.assertEqual(got, {"a": "same", "b": "changed", "l": "lines", "gone": "removed", "new": "added"})


class ElfDigestsTest(unittest.TestCase):
    def test_rejects_other_formats(self):
        with tempfile.NamedTemporaryFile() as fh:
            fh.write(b"#!/bin/sh\n")
            fh.flush()
            with self.assertRaisesRegex(RuntimeError, "not a 64-bit little-endian ELF"):
                br.elf_digests(fh.name)


def sample_report(code):
    base = FakeTree({"apid": APID, "vmmd": VMMD, "meterd": APID}, FILES)
    closure = {"apid": ["pkg/api/api.go"], "meterd": ["pkg/api/api.go", "a", "b", "c"]}
    lanes = {"apid": {"lane": "control-plane"}, "vmmd": {"lane": "compute"}}
    return br.assemble(base, base, "m" * 40, ["pkg/api/api.go"], closure, code, lanes)


def table_names(md):
    """The binary column of the table rows in md."""
    return [line.split(" | ")[0][2:] for line in md.splitlines()
            if line.startswith("| ") and "---" not in line and not line.startswith("| binary |")]


class ReportTest(unittest.TestCase):
    def test_assemble(self):
        rep = sample_report({"apid": "changed", "meterd": "lines"})
        self.assertEqual(rep["closure"], ["apid", "meterd"])
        self.assertEqual(rep["code"], ["apid"])
        self.assertEqual(rep["lines"], ["meterd"])
        self.assertEqual(rep["binaries"]["vmmd"], {"lane": "compute", "closure": False, "reasons": [],
                                                    "reason_count": 0, "code": "same"})
        self.assertEqual(rep["binaries"]["meterd"]["lane"], "unlisted")
        self.assertEqual(rep["binaries"]["meterd"]["reasons"], ["pkg/api/api.go", "a", "b"])
        self.assertEqual(rep["binaries"]["meterd"]["reason_count"], 4)

    def test_broken_merge_base_counts_every_built_binary_as_changed(self):
        # Two merged PRs left main uncompilable; the PR that repairs it must
        # still get a report, not a build error at the merge base.
        base = FakeTree({"apid": APID, "meterd": APID}, FILES, sha="a" * 40)
        head = FakeTree({"apid": APID}, FILES, sha="c" * 40)

        def fake_build(side, names, out):
            if side is base:
                raise RuntimeError("go build at aaaaaaaaaaaa: routes.go:179:2: duplicate key\nmore")
            return {n: ("f", "c") for n in names}
        code, err = br.measure_code(base, head, ["apid", "meterd"], "out", fake_build)
        self.assertEqual(code, {"apid": "changed", "meterd": "removed"})
        self.assertEqual(err, "go build at aaaaaaaaaaaa: routes.go:179:2: duplicate key")
        rep = sample_report(code)
        rep["base_build_error"] = err
        self.assertIn("The merge base does not build", br.markdown(rep))

        def head_broken(side, names, out):
            if side is head:
                raise RuntimeError("go build at cccccccccccc: broken head")
            return {n: ("f", "c") for n in names}
        with self.assertRaisesRegex(RuntimeError, "broken head"):
            br.measure_code(base, head, ["apid"], "out", head_broken)

    def test_assemble_without_builds(self):
        rep = sample_report(None)
        self.assertIsNone(rep["code"])
        self.assertIsNone(rep["lines"])
        self.assertIsNone(rep["binaries"]["apid"]["code"])

    def test_markdown_shows_changed_code_and_folds_the_rest(self):
        md = br.markdown(sample_report({"apid": "same", "meterd": "changed"}))
        self.assertTrue(md.startswith(br.MARKER + "\n"))
        self.assertIn("the code of **1 of 3** release binaries changes: meterd.", md)
        head, folded = md.split("<details>")
        self.assertEqual(table_names(head), ["meterd"])
        self.assertIn("(+1 more)", head)
        self.assertIn("<summary>1 more rebuilt with the same code: a unit release does not restart them</summary>",
                      folded)
        self.assertEqual(table_names(folded), ["apid"])
        self.assertNotIn("vmmd", md)

    def test_markdown_counts_line_table_only_binaries(self):
        md = br.markdown(sample_report({"apid": "lines", "meterd": "changed"}))
        self.assertIn("1 more rebuilt with the same code, 1 with moved line numbers only:", md)
        self.assertEqual(table_names(md.split("<details>")[1]), ["apid"])

    def test_markdown_when_no_code_changes(self):
        md = br.markdown(sample_report({"apid": "same", "meterd": "lines"}))
        self.assertIn("no release binary's code changes.", md)
        head, folded = md.split("<details>")
        self.assertEqual(table_names(head), [])
        self.assertEqual(table_names(folded), ["meterd", "apid"])

    def test_markdown_without_changes(self):
        rep = sample_report({})
        rep["closure"] = []
        self.assertIn("no release binary's inputs changed", br.markdown(rep))

    def test_markdown_without_builds(self):
        md = br.markdown(sample_report(None))
        self.assertIn("code was not measured", md)
        self.assertNotIn("<details>", md)
        self.assertEqual(table_names(md), ["apid", "meterd"])


class RepositoryContractTest(unittest.TestCase):
    """build() mirrors the Makefile's release recipe; keep them in step."""

    def test_release_recipe_flags_are_the_ones_build_uses(self):
        makefile = (REPO / "Makefile").read_text(encoding="utf-8")
        recipe = au.build_recipe(makefile)
        self.assertEqual(au.makefile_list(makefile, "GO_BUILD_FLAGS"), ["-trimpath", "-buildvcs=false"])
        self.assertIn("BUILD_CGO_ENABLED ?= 0", recipe)
        self.assertRegex(recipe, r"\$\(GO_BUILD_FLAGS\) -tags (metal|\$\(DAEMON_TAGS\)) \\\n\s+-ldflags '\$\(LDFLAGS\) -s -w' "
                                 r"-o \$\(BINDIR\)/ \$\(addprefix \./cmd/,\$\(DAEMONS\)\)")
        self.assertRegex(recipe, r"\$\(GO_BUILD_FLAGS\) \\\n\s+-ldflags '\$\(LDFLAGS\) -s -w' "
                                 r"-o \$\(BINDIR\)/ \$\(addprefix \./cmd/,\$\(CLIS\)\)")
        self.assertIn(f"LDFLAGS := -X github.com/onebox-faas/faas/{br.VERSION_VAR}=$(VERSION)", recipe)


def lib(a="1", b="2", above_b=""):
    """pkg/lib with two functions the linker keeps out of line, so their
    code and line-table rows stay their own."""
    return (f"package lib\n\n//go:noinline\nfunc A() int {{ return {a} }}\n\n"
            f"{above_b}//go:noinline\nfunc B() int {{ return {b} }}\n")


@unittest.skipUnless(shutil.which(os.environ.get("GO", "go")), "go toolchain not available")
class EndToEndTest(unittest.TestCase):
    """report() on a throwaway module: real go list, real builds, real git."""

    def run_git(self, *args):
        return subprocess.run([*GIT, *args], cwd=self.root, check=True, capture_output=True, text=True).stdout

    def write(self, path, body):
        full = os.path.join(self.root, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as fh:
            fh.write(body)

    def commit(self, message):
        self.run_git("add", "-A")
        self.run_git("commit", "-q", "-m", message)
        return self.run_git("rev-parse", "HEAD").strip()

    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root)
        os.environ.setdefault("GOTOOLCHAIN", "local")
        os.environ.setdefault("GOFLAGS", "")
        self.run_git("init", "-q", "-b", "main")
        self.run_git("config", "user.email", "t@example.com")
        self.run_git("config", "user.name", "t")
        self.write("go.mod", "module example.com/m\n\ngo 1.21\n")
        self.write("Makefile", "LDFLAGS := -X example.com/m/pkg/wire.Version=$(VERSION)\n"
                               "GO_BUILD_FLAGS := -trimpath -buildvcs=false\n"
                               "DAEMONS := apid vmmd\nCLIS := gregalectl\n\n"
                               "build-release-batch:\n\tgo build $(GO_BUILD_FLAGS)\n")
        self.write("pkg/wire/wire.go", 'package wire\n\nvar Version = "dev"\n')
        # apid calls lib.A and vmmd calls lib.B: both import pkg/lib, and the
        # linker keeps only the function each one reaches.
        self.write("cmd/apid/main.go", 'package main\n\nimport (\n\t"example.com/m/pkg/lib"\n'
                                       '\t"example.com/m/pkg/wire"\n)\n\nfunc main() { println(lib.A(), wire.Version) }\n')
        self.write("cmd/vmmd/main.go", 'package main\n\nimport "example.com/m/pkg/lib"\n\nfunc main() { println(lib.B()) }\n')
        self.write("cmd/gregalectl/main.go", "package main\n\nfunc main() {}\n")
        self.write("pkg/lib/lib.go", lib())
        self.config = os.path.join(self.root, "lanes.json")
        with open(self.config, "w", encoding="utf-8") as fh:
            json.dump({"binaries": {"apid": {"lane": "control-plane", "services": [{"name": "apid"}]},
                                    "vmmd": {"lane": "compute"}, "gregalectl": {"lane": "tool"}},
                       "global_inputs": ["go.mod", "go.sum"]}, fh)
        self.base = self.commit("base")

    def report(self, base, head="HEAD", **kw):
        return br.report(base, head, root=self.root, config_path=self.config, workdir=self.root, **kw)

    def test_code_narrows_the_closure(self):
        # Same line count, so apid's pclntab line numbers do not move.
        self.write("pkg/lib/lib.go", lib(b="3"))
        self.commit("change B")
        rep = self.report(self.base)
        self.assertEqual(rep["closure"], ["apid", "vmmd"])
        self.assertEqual(rep["code"], ["vmmd"])
        self.assertEqual(rep["binaries"]["apid"]["code"], "same")
        self.assertEqual(rep["binaries"]["gregalectl"], {"lane": "tool", "closure": False, "reasons": [],
                                                         "reason_count": 0, "code": "same"})
        self.assertEqual(rep["binaries"]["vmmd"]["reasons"], ["pkg/lib/lib.go"])

    def test_every_binary_agrees_with_the_closure_prefilter(self):
        self.write("pkg/lib/lib.go", lib(a="4"))
        self.commit("change A")
        rep = self.report(self.base, every=True)
        self.assertEqual(rep["code"], ["apid"])
        self.assertEqual(rep["binaries"]["gregalectl"]["code"], "same")

    def test_range_starts_at_the_merge_base(self):
        self.write("pkg/lib/lib.go", lib(a="5"))
        self.commit("main moves on")
        main = self.run_git("rev-parse", "HEAD").strip()
        self.run_git("checkout", "-q", "-b", "topic", self.base)
        self.write("cmd/gregalectl/main.go", "package main\n\nfunc main() { println(1) }\n")
        head = self.commit("topic change")
        rep = self.report(main, head)
        self.assertEqual(rep["merge_base"], self.base)
        self.assertEqual(rep["closure"], ["gregalectl"])
        self.assertEqual(rep["code"], ["gregalectl"])

    def test_a_line_shift_changes_only_the_line_table(self):
        self.write("pkg/lib/lib.go", lib(above_b="// B moved down a line.\n"))
        self.commit("comment above B")
        rep = self.report(self.base)
        self.assertEqual(rep["code"], [])
        self.assertEqual(rep["lines"], ["vmmd"])
        self.assertEqual(rep["binaries"]["apid"]["code"], "same")

    def test_extra_tags_reach_closures_and_builds(self):
        self.write("pkg/lib/tagged.go", "//go:build blast\n\npackage lib\n\nfunc init() { println(9) }\n")
        self.commit("tagged file")
        self.assertEqual(self.report(self.base)["closure"], [])
        rep = self.report(self.base, extra_tags=["blast"])
        self.assertEqual(rep["closure"], ["apid", "vmmd"])
        self.assertEqual(rep["code"], ["apid", "vmmd"])

    def test_closure_only_builds_nothing(self):
        self.write("pkg/lib/lib.go", lib(b="6"))
        self.commit("change B")
        rep = self.report(self.base, measure=False)
        self.assertEqual(rep["closure"], ["apid", "vmmd"])
        self.assertIsNone(rep["code"])

    def test_build_failure_names_the_commit(self):
        self.write("pkg/lib/lib.go", lib(a=""))
        head = self.commit("broken")
        with self.assertRaisesRegex(RuntimeError, f"go build at {head[:12]}"):
            self.report(self.base)


if __name__ == "__main__":
    unittest.main()
