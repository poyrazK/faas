#!/usr/bin/env python3
"""Exercise read-only native preflight guards using an entirely owned fake host."""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SOURCE = Path(__file__).resolve().parents[1] / "test-managed-service-native.sh"


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        scripts = self.root / "scripts"
        scripts.mkdir()
        for name in ("kvm", "designated", "kernel", "builder", "guest", "minimal", "minimal.digest"):
            (self.root / name).write_text("owned fixture\n")
        (self.root / "guest").chmod(0o700)
        # Replace only host designation/device probes in a private copy. Never
        # create a KVM device, alter /etc, invoke guests or modify host networking.
        source = SOURCE.read_text().replace("/dev/kvm", str(self.root / "kvm"))
        source = source.replace("/etc/faas/builder-acceptance-host", str(self.root / "designated"))
        self.runner = scripts / "test-managed-service-native.sh"
        self.runner.write_text(source)
        self.tool("id", "echo 0")
        self.tool("uname", 'case "$1" in -s) echo Linux;; -m) echo x86_64;; *) exit 99;; esac')
        self.tool("systemd-detect-virt", '''
case "$1" in
  --container) echo "${SERVICE_TEST_CONTAINER:-none}"; exit "${SERVICE_TEST_CONTAINER_STATUS:-1}";;
  --vm) echo "${SERVICE_TEST_VM:-none}"; exit "${SERVICE_TEST_VM_STATUS:-1}";;
  *) exit 99;;
esac''')
        self.tool("systemctl", '[ "${SERVICE_TEST_ACTIVE_SERVICE:-}" = "$3" ]')
        self.tool("pgrep", 'exit "${SERVICE_TEST_PGREP:-1}"')
        self.tool("git", '''
case "$1" in
  rev-parse) echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa;;
  status) if [ "${SERVICE_TEST_DIRTY:-}" = 1 ]; then echo ' M owned-source'; fi;;
  *) exit 99;;
esac''')
        self.tool("ip", '''
if [ "$1" != -4 ]; then echo 'unexpected host mutation' >&2; exit 99; fi
if [ "${SERVICE_TEST_OCCUPIED:-}" = 1 ]; then echo 'owned address already assigned'; fi''')
        for name in ("make", "firecracker", "jailer", "flock", "nft"):
            self.tool(name, "echo 'unexpected preflight mutation' >&2; exit 99")
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        DATABASE_URL="postgres://unused.invalid/disposable",
                        FAAS_TEST_KERNEL=str(self.root / "kernel"),
                        FAAS_BUILDER_BASE_PATH=str(self.root / "builder"),
                        FAAS_TEST_MINIMAL_BASE_PATH=str(self.root / "minimal"),
                        FAAS_GUEST_INIT=str(self.root / "guest"), FAAS_SKIP_PG_TESTS="",
                        GREGALE_SERVICE_NATIVE_HOST_CLASS="native",
                        GREGALE_SERVICE_NATIVE_REPORT_PATH="", GREGALE_SERVICE_NATIVE_RESULTS_PATH="")

    def tool(self, name, body):
        path = self.bin / name
        path.write_text("#!/bin/sh\n" + body + "\n")
        path.chmod(0o700)

    def run_preflight(self, **changes):
        return subprocess.run(["sh", str(self.runner), "--preflight"], env=dict(self.env, **changes),
                              capture_output=True, text=True, timeout=10)

    def test_native_preflight_is_read_only(self):
        result = self.run_preflight()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no daemons or guests started", result.stdout)

    def test_nested_preflight_is_explicitly_diagnostic(self):
        result = self.run_preflight(GREGALE_SERVICE_NATIVE_HOST_CLASS="nested-diagnostic", SERVICE_TEST_VM="kvm", SERVICE_TEST_VM_STATUS="0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("cannot qualify native service", result.stdout)

    def test_vm_container_and_unknown_detection_cannot_claim_native(self):
        for changes in (dict(SERVICE_TEST_VM="kvm", SERVICE_TEST_VM_STATUS="0"),
                        dict(SERVICE_TEST_VM="", SERVICE_TEST_VM_STATUS="2"),
                        dict(SERVICE_TEST_CONTAINER_STATUS="0"), dict(SERVICE_TEST_CONTAINER_STATUS="2")):
            with self.subTest(changes=changes):
                self.assertNotEqual(self.run_preflight(**changes).returncode, 0)

    def test_dirty_busy_and_skipped_sql_are_rejected(self):
        for changes in (dict(SERVICE_TEST_DIRTY="1"), dict(SERVICE_TEST_ACTIVE_SERVICE="faas-vmmd"),
                        dict(FAAS_SKIP_PG_TESTS="1"), dict(SERVICE_TEST_PGREP="0"), dict(SERVICE_TEST_PGREP="2"),
                        dict(GREGALE_SERVICE_NATIVE_HOST_CLASS="unknown")):
            with self.subTest(changes=changes):
                self.assertNotEqual(self.run_preflight(**changes).returncode, 0)

    def test_missing_device_designation_and_fixtures_are_rejected(self):
        for name in ("kvm", "designated", "kernel", "builder", "guest", "minimal", "minimal.digest"):
            with self.subTest(name=name):
                path = self.root / name
                saved = path.read_bytes()
                path.unlink()
                self.assertNotEqual(self.run_preflight().returncode, 0)
                path.write_bytes(saved)
                if name == "guest":
                    path.chmod(0o700)

    def test_existing_evidence_and_symlinks_are_preserved(self):
        path = self.root / "existing.json"
        path.write_text("previous evidence")
        link = self.root / "link.json"
        link.symlink_to(path)
        for candidate in (path, link):
            with self.subTest(candidate=str(candidate)):
                self.assertNotEqual(self.run_preflight(GREGALE_SERVICE_NATIVE_REPORT_PATH=str(candidate)).returncode, 0)
                self.assertEqual(path.read_text(), "previous evidence")


if __name__ == "__main__":
    unittest.main()
