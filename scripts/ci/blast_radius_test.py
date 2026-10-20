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


class CompareTest(unittest.TestCase):
    def test_compare(self):
        got = br.compare({"a": "1", "b": "2", "gone": "3"}, {"a": "1", "b": "9", "new": "4"},
                         ["a", "b", "gone", "new"])
        self.assertEqual(got, {"a": "same", "b": "changed", "gone": "removed", "new": "added"})


def sample_report(code):
    base = FakeTree({"apid": APID, "vmmd": VMMD, "meterd": APID}, FILES)
    closure = {"apid": ["pkg/api/api.go"], "meterd": ["pkg/api/api.go", "a", "b", "c"]}
    lanes = {"apid": {"lane": "control-plane"}, "vmmd": {"lane": "compute"}}
    return br.assemble(base, base, "m" * 40, ["pkg/api/api.go"], closure, code, lanes)


class ReportTest(unittest.TestCase):
    def test_assemble(self):
        rep = sample_report({"apid": "changed", "meterd": "same"})
        self.assertEqual(rep["closure"], ["apid", "meterd"])
        self.assertEqual(rep["code"], ["apid"])
        self.assertEqual(rep["binaries"]["vmmd"], {"lane": "compute", "closure": False, "reasons": [],
                                                    "reason_count": 0, "code": "same"})
        self.assertEqual(rep["binaries"]["meterd"]["lane"], "unlisted")
        self.assertEqual(rep["binaries"]["meterd"]["reasons"], ["pkg/api/api.go", "a", "b"])
        self.assertEqual(rep["binaries"]["meterd"]["reason_count"], 4)

    def test_assemble_without_builds(self):
        rep = sample_report(None)
        self.assertIsNone(rep["code"])
        self.assertIsNone(rep["binaries"]["apid"]["code"])

    def test_markdown_lists_closure_binaries_changed_first(self):
        md = br.markdown(sample_report({"apid": "same", "meterd": "changed"}))
        self.assertTrue(md.startswith(br.MARKER + "\n"))
        self.assertIn("the code of **1 of 3** release binaries changes", md)
        self.assertIn("ships **2 of 3**", md)
        rows = [line for line in md.splitlines() if line.startswith("| ") and "---" not in line][1:]
        self.assertEqual([r.split(" | ")[0] for r in rows], ["| meterd", "| apid"])
        self.assertIn("(+1 more)", rows[0])
        self.assertNotIn("vmmd", md)

    def test_markdown_without_changes(self):
        rep = sample_report({})
        rep["closure"] = []
        self.assertIn("no release binary's inputs changed", br.markdown(rep))

    def test_markdown_without_builds(self):
        self.assertIn("code was not measured", br.markdown(sample_report(None)))


class RepositoryContractTest(unittest.TestCase):
    """build() mirrors the Makefile's release recipe; keep them in step."""

    def test_release_recipe_flags_are_the_ones_build_uses(self):
        makefile = (REPO / "Makefile").read_text(encoding="utf-8")
        recipe = au.build_recipe(makefile)
        self.assertEqual(au.makefile_list(makefile, "GO_BUILD_FLAGS"), ["-trimpath", "-buildvcs=false"])
        self.assertIn("BUILD_CGO_ENABLED ?= 0", recipe)
        self.assertRegex(recipe, r"\$\(GO_BUILD_FLAGS\) -tags metal \\\n\s+-ldflags '\$\(LDFLAGS\) -s -w' "
                                 r"-o \$\(BINDIR\)/ \$\(addprefix \./cmd/,\$\(DAEMONS\)\)")
        self.assertRegex(recipe, r"\$\(GO_BUILD_FLAGS\) \\\n\s+-ldflags '\$\(LDFLAGS\) -s -w' "
                                 r"-o \$\(BINDIR\)/ \$\(addprefix \./cmd/,\$\(CLIS\)\)")
        self.assertIn(f"LDFLAGS := -X github.com/onebox-faas/faas/{br.VERSION_VAR}=$(VERSION)", recipe)


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
        self.write("pkg/lib/lib.go", "package lib\n\nfunc A() int { return 1 }\n\nfunc B() int { return 2 }\n")
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
        self.write("pkg/lib/lib.go", "package lib\n\nfunc A() int { return 1 }\n\nfunc B() int { return 3 }\n")
        self.commit("change B")
        rep = self.report(self.base)
        self.assertEqual(rep["closure"], ["apid", "vmmd"])
        self.assertEqual(rep["code"], ["vmmd"])
        self.assertEqual(rep["binaries"]["apid"]["code"], "same")
        self.assertEqual(rep["binaries"]["gregalectl"], {"lane": "tool", "closure": False, "reasons": [],
                                                         "reason_count": 0, "code": "same"})
        self.assertEqual(rep["binaries"]["vmmd"]["reasons"], ["pkg/lib/lib.go"])

    def test_every_binary_agrees_with_the_closure_prefilter(self):
        self.write("pkg/lib/lib.go", "package lib\n\nfunc A() int { return 4 }\n\nfunc B() int { return 2 }\n")
        self.commit("change A")
        rep = self.report(self.base, every=True)
        self.assertEqual(rep["code"], ["apid"])
        self.assertEqual(rep["binaries"]["gregalectl"]["code"], "same")

    def test_range_starts_at_the_merge_base(self):
        self.write("pkg/lib/lib.go", "package lib\n\nfunc A() int { return 5 }\n\nfunc B() int { return 2 }\n")
        self.commit("main moves on")
        main = self.run_git("rev-parse", "HEAD").strip()
        self.run_git("checkout", "-q", "-b", "topic", self.base)
        self.write("cmd/gregalectl/main.go", "package main\n\nfunc main() { println(1) }\n")
        head = self.commit("topic change")
        rep = self.report(main, head)
        self.assertEqual(rep["merge_base"], self.base)
        self.assertEqual(rep["closure"], ["gregalectl"])
        self.assertEqual(rep["code"], ["gregalectl"])

    def test_closure_only_builds_nothing(self):
        self.write("pkg/lib/lib.go", "package lib\n\nfunc A() int { return 1 }\n\nfunc B() int { return 6 }\n")
        self.commit("change B")
        rep = self.report(self.base, measure=False)
        self.assertEqual(rep["closure"], ["apid", "vmmd"])
        self.assertIsNone(rep["code"])

    def test_build_failure_names_the_commit(self):
        self.write("pkg/lib/lib.go", "package lib\n\nfunc A() int { return }\n\nfunc B() int { return 2 }\n")
        head = self.commit("broken")
        with self.assertRaisesRegex(RuntimeError, f"go build at {head[:12]}"):
            self.report(self.base)


if __name__ == "__main__":
    unittest.main()
