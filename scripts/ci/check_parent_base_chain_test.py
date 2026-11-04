#!/usr/bin/env python3
"""Tests for check_parent_base_chain.py."""

import importlib.util
import pathlib
import unittest

SPEC = importlib.util.spec_from_file_location(
    "check_parent_base_chain", pathlib.Path(__file__).with_name("check_parent_base_chain.py"))
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)


def lock(**digests):
    names = {"parent": check.PARENT_DOCKERFILE, "node": "images/runner-node24.Dockerfile"}
    return {"pinned": [{"dockerfile": names[key], "resolved_repo": f"mirror.gcr.io/library/{key}",
                        "digest": digest} for key, digest in digests.items()]}


class ChildDockerfileTests(unittest.TestCase):
    def test_reads_parent_rows_from_default_runtime_base_refs(self):
        source = (check.ROOT / "pkg/imaged/base_stage.go").read_text()
        self.assertEqual(sorted(check.child_dockerfiles(source)),
                         ["images/runner-node24.Dockerfile", "images/runner-python312.Dockerfile"])


class ChainProblemTests(unittest.TestCase):
    layers = {
        "mirror.gcr.io/library/parent@sha256:new": ["sha256:debian-new"],
        "mirror.gcr.io/library/node@sha256:new": ["sha256:debian-new", "sha256:node"],
        # The regression from #5241: the child kept an upstream built on an
        # older Debian snapshot after the parent moved.
        "mirror.gcr.io/library/node@sha256:old": ["sha256:debian-old", "sha256:node"],
    }

    def test_accepts_child_built_on_the_parent_snapshot(self):
        problems = check.chain_problems(lock(parent="sha256:new", node="sha256:new"),
                                        ["images/runner-node24.Dockerfile"], self.layers.__getitem__)
        self.assertEqual(problems, [])

    def test_rejects_child_on_another_snapshot(self):
        problems = check.chain_problems(lock(parent="sha256:new", node="sha256:old"),
                                        ["images/runner-node24.Dockerfile"], self.layers.__getitem__)
        self.assertEqual(len(problems), 1)
        self.assertIn("images/runner-node24.Dockerfile", problems[0])

    def test_requires_one_lock_entry_per_dockerfile(self):
        with self.assertRaises(ValueError):
            check.chain_problems(lock(parent="sha256:new"), ["images/runner-node24.Dockerfile"],
                                 self.layers.__getitem__)


if __name__ == "__main__":
    unittest.main()
