#!/usr/bin/env python3
"""Exercise native preflight on owned fixtures, without devices or host mutation."""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]


class HostTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / "scripts/ci").mkdir(parents=True)
        (self.root / ".git").mkdir()
        self.bin = self.root / "tools"
        self.bin.mkdir()
        for name in ("kvm", "designation", "ca", "kernel", "builder", "minimal", "minimal.digest", "go"):
            (self.root / name).write_text("owned fixture\n")
        (self.root / "go").chmod(0o700)
        self.config = self.root / "bindings.env"
        self.config.write_text("\n".join([
            "DATABASE_URL='postgres://unused.invalid/disposable'",
            "GREGALE_POSTGRES_NATIVE_CA_PATH='" + str(self.root / "ca") + "'",
            "FAAS_TEST_KERNEL='" + str(self.root / "kernel") + "'",
            "FAAS_TEST_MINIMAL_BASE_PATH='" + str(self.root / "minimal") + "'",
            "FAAS_BUILDER_BASE_PATH='" + str(self.root / "builder") + "'",
        ]))
        script = (REPO / "scripts/ci/run-binding-native.sh").read_text()
        script = script.replace("/dev/kvm", str(self.root / "kvm"))
        script = script.replace("/etc/faas/builder-acceptance-host", str(self.root / "designation"))
        script = script.replace("config=/etc/faas/bindings-native.env", "config=" + str(self.config))
        self.runner = self.root / "scripts/ci/run-binding-native.sh"
        self.runner.write_text(script)
        self.tool("id", "echo 0")
        self.tool("uname", 'case "$1" in -s) echo Linux;; -m) echo x86_64;; *) exit 99;; esac')
        self.tool("stat", 'echo "${BINDING_TEST_PERMS:-600 0}"')
        self.tool("systemd-detect-virt", 'echo "${BINDING_TEST_VIRT:-none}"; exit "${BINDING_TEST_VIRT_STATUS:-1}"')
        self.tool("systemctl", '[ "${BINDING_TEST_ACTIVE:-}" = "$3" ]')
        self.tool("pgrep", 'exit "${BINDING_TEST_PGREP:-1}"')
        self.tool("git", '''case "$1" in
rev-parse) echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa;;
status) if [ "${BINDING_TEST_DIRTY:-}" = 1 ]; then echo ' M owned'; fi;;
*) exit 99;; esac''')
        self.tool("make", "echo 'unexpected host mutation' >&2; exit 99")
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        FAAS_E2E_SOURCE_SHA="a" * 40, FAAS_E2E_GO=str(self.root / "go"),
                        FAAS_E2E_TRANSFER_ROOT=str(self.root), FAAS_SKIP_PG_TESTS="")

    def tool(self, name, body):
        path = self.bin / name
        path.write_text("#!/bin/sh\n" + body + "\n")
        path.chmod(0o700)

    def preflight(self, **changes):
        return subprocess.run(["bash", str(self.runner), "--preflight"], env=dict(self.env, **changes), capture_output=True, text=True, timeout=10)

    def test_valid_preflight_starts_nothing(self):
        result = self.preflight()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no builds, daemons or guests started", result.stdout)
        self.assertFalse((self.root / "binding-guest-init").exists())

    def test_virtual_unknown_busy_dirty_or_skipped_context_is_refused(self):
        for changes in (dict(BINDING_TEST_VIRT="kvm", BINDING_TEST_VIRT_STATUS="0"),
                        dict(BINDING_TEST_VIRT_STATUS="2"), dict(BINDING_TEST_ACTIVE="faas-vmmd"),
                        dict(BINDING_TEST_PGREP="0"), dict(BINDING_TEST_PGREP="2"),
                        dict(BINDING_TEST_DIRTY="1"), dict(FAAS_SKIP_PG_TESTS="1"),
                        dict(FAAS_E2E_SOURCE_SHA="b" * 40)):
            with self.subTest(changes=changes):
                self.assertNotEqual(self.preflight(**changes).returncode, 0)

    def test_private_configuration_and_all_fixtures_are_required(self):
        for perms in ("644 0", "600 1000"):
            self.assertNotEqual(self.preflight(BINDING_TEST_PERMS=perms).returncode, 0)
        for name in ("kvm", "designation", "ca", "kernel", "builder", "minimal", "minimal.digest", "go", "bindings.env"):
            path = self.root / name
            saved = path.read_bytes()
            mode = path.stat().st_mode
            path.unlink()
            with self.subTest(name=name):
                self.assertNotEqual(self.preflight().returncode, 0)
            path.write_bytes(saved)
            path.chmod(mode)
        original = self.config.read_bytes()
        target = self.root / "original.env"
        target.write_bytes(original)
        self.config.unlink()
        self.config.symlink_to(target)
        self.assertNotEqual(self.preflight().returncode, 0)


if __name__ == "__main__":
    unittest.main()
