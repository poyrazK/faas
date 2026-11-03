"""Tests images/agent-base/finish-install.sh against a fixture node_modules.

Run with: python3 images/agent_base_install_test.py
"""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "agent-base" / "finish-install.sh"
CATALOG = Path(__file__).resolve().parent.parent / "pkg" / "agentpresets" / "presets.json"


def fake_binary(path: Path, version: str, marker: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(f"#!/bin/sh\n# {marker}\necho 'tool {version}'\n")
    path.chmod(0o755)


class FinishInstallTest(unittest.TestCase):
    def setUp(self) -> None:
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.root)
        self.modules = self.root / "node_modules"
        self.bindir = self.root / "bin"
        self.bindir.mkdir()
        presets = {p["name"]: p for p in json.loads(CATALOG.read_text())["presets"]}
        m = self.modules
        # What npm ci plus both postinstalls leave: one hard link per binary.
        for variant in ("opencode-linux-x64", "opencode-linux-x64-baseline",
                        "opencode-linux-x64-musl", "opencode-linux-x64-baseline-musl"):
            fake_binary(m / variant / "bin" / "opencode", presets["opencode"]["version"], variant)
        (m / "opencode-ai" / "bin").mkdir(parents=True)
        os.link(m / "opencode-linux-x64-musl" / "bin" / "opencode", m / "opencode-ai" / "bin" / "opencode.exe")
        fake_binary(m / "@anthropic-ai" / "claude-code-linux-x64-musl" / "claude", presets["claude-code"]["version"], "claude")
        (m / "@anthropic-ai" / "claude-code" / "bin").mkdir(parents=True)
        os.link(m / "@anthropic-ai" / "claude-code-linux-x64-musl" / "claude", m / "@anthropic-ai" / "claude-code" / "bin" / "claude.exe")
        fake_binary(m / "@openai" / "codex" / "bin" / "codex.js", presets["codex"]["version"], "codex")
        (m / ".bin").mkdir()
        for name, target in (("codex", "../@openai/codex/bin/codex.js"),
                             ("opencode", "../opencode-ai/bin/opencode.exe"),
                             ("claude", "../@anthropic-ai/claude-code/bin/claude.exe")):
            os.symlink(target, m / ".bin" / name)

    def run_script(self) -> subprocess.CompletedProcess:
        env = dict(os.environ, AGENT_BASE_MODULES=str(self.modules), AGENT_BASE_BINDIR=str(self.bindir),
                   AGENT_BASE_CATALOG=str(CATALOG))
        return subprocess.run(["sh", str(SCRIPT)], env=env, capture_output=True, text=True, timeout=60)

    def test_links_prunes_and_checks_versions(self) -> None:
        out = self.run_script()
        self.assertEqual(out.returncode, 0, out.stderr)
        left = sorted(p.name for p in self.modules.glob("opencode-linux-*"))
        self.assertEqual(left, ["opencode-linux-x64-musl"])
        for name in ("codex", "opencode", "claude"):
            self.assertTrue((self.bindir / name).is_symlink(), name)
        self.assertEqual(out.stdout.count(" ok"), 3, out.stdout)

    def test_refuses_an_opencode_binary_from_outside_the_lock(self) -> None:
        # The registry fallback copies a binary that no locked variant holds.
        exe = self.modules / "opencode-ai" / "bin" / "opencode.exe"
        exe.unlink()
        fake_binary(exe, "1.19.3", "downloaded")
        out = self.run_script()
        self.assertNotEqual(out.returncode, 0)
        self.assertIn("matches no locked variant", out.stderr)
        self.assertEqual(len(list(self.modules.glob("opencode-linux-*"))), 4, "nothing pruned on failure")

    def test_refuses_a_changed_claude_binary(self) -> None:
        exe = self.modules / "@anthropic-ai" / "claude-code" / "bin" / "claude.exe"
        exe.unlink()
        fake_binary(exe, "2.1.302", "patched")
        out = self.run_script()
        self.assertNotEqual(out.returncode, 0)
        self.assertIn("not the locked platform binary", out.stderr)

    def test_refuses_a_version_the_catalog_does_not_pin(self) -> None:
        codex = self.modules / "@openai" / "codex" / "bin" / "codex.js"
        fake_binary(codex, "0.1.0", "codex")
        out = self.run_script()
        self.assertNotEqual(out.returncode, 0)
        self.assertIn("want 0.163.0", out.stderr)


if __name__ == "__main__":
    unittest.main()
