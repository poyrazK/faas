#!/usr/bin/env python3
"""spec: §14.A; adr: 1530 — fault guards; all host calls are mocked."""
import importlib.util
import json
from pathlib import Path
import signal
import stat
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch
import uuid

SPEC = importlib.util.spec_from_file_location("peer", Path(__file__).with_name("native_m9_peer.py"))
peer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(peer)


class PeerTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.lease = Path(self.directory.name) / "owner.json"
        self.marker = Path(self.directory.name) / "marker"
        self.marker.touch(mode=0o600)
        self.request = {"run_id": str(uuid.uuid4()), "vmmd_sha256": "a" * 64, "action": "claim"}
        self.identity = {"pid": 123, "invocation_id": "b" * 32, "start_ticks": "42", "vmmd_sha256": "a" * 64}
        for target, value in (("LEASE", self.lease), ("MARKER", self.marker)):
            p = patch.object(peer, target, value)
            p.start()
            self.addCleanup(p.stop)

    def data(self):
        return dict(self.request, initial=self.identity, paused=None)

    def test_foreign_run_and_existing_lease_cannot_be_taken_over(self):
        self.lease.write_text(json.dumps(self.data()))
        self.lease.chmod(0o600)
        foreign = dict(self.request, run_id=str(uuid.uuid4()))
        lease = Mock(wraps=self.lease)
        lease.lstat.return_value = SimpleNamespace(st_mode=stat.S_IFREG | 0o600, st_uid=0, st_nlink=1)
        with patch.object(peer, "LEASE", lease):
            self.assertEqual(peer.load_lease(self.request)["run_id"], self.request["run_id"])
            with self.assertRaisesRegex(ValueError, "different M9 run"):
                peer.load_lease(foreign)
        with patch.object(peer, "native_host", return_value={}), patch.object(peer, "identity", return_value=self.identity):
            with self.assertRaises(FileExistsError):
                peer.claim(self.request)

    def test_signals_use_pidfd_and_refuse_replaced_invocations(self):
        with patch.object(peer.os, "pidfd_open", return_value=99), patch.object(peer.os, "close") as close, \
             patch.object(peer.signal, "pidfd_send_signal") as send, \
             patch.object(peer, "identity", return_value=dict(self.identity, invocation_id="c" * 32)):
            with self.assertRaises(ValueError):
                peer.fenced_signal(self.identity, signal.SIGKILL)
            send.assert_not_called()
            close.assert_called_once_with(99)
        with patch.object(peer.os, "pidfd_open", return_value=99), patch.object(peer.os, "close"), \
             patch.object(peer.signal, "pidfd_send_signal") as send, \
             patch.object(peer, "identity", return_value=self.identity):
            peer.fenced_signal(self.identity, signal.SIGCONT)
            send.assert_called_once_with(99, signal.SIGCONT)

    def test_failed_resume_retains_recovery_identity(self):
        data = dict(self.data(), paused=self.identity)
        peer.save_lease(data)
        with patch.object(peer, "fenced_signal", side_effect=ValueError("invocation changed")):
            with self.assertRaises(ValueError):
                peer.resume(self.request, data)
        self.assertEqual(json.loads(self.lease.read_text())["paused"], self.identity)

    def test_release_needs_current_identity_and_successful_independent_leakcheck(self):
        peer.save_lease(self.data())
        request = dict(self.request, action="release")
        with patch.object(peer, "validate"), patch.object(peer, "load_lease", return_value=self.data()), \
             patch.object(peer, "identity", return_value=self.identity), \
             patch.object(peer, "leakcheck", side_effect=ValueError("leaked loop")) as check:
            with self.assertRaisesRegex(ValueError, "leaked loop"):
                peer.execute(request)
            check.assert_called_once()
        self.assertTrue(self.lease.exists())

    def test_pause_persists_recovery_identity_before_signal_delivery(self):
        def stopped(expected, sig):
            self.assertEqual(sig, signal.SIGSTOP)
            self.assertEqual(json.loads(self.lease.read_text())["paused"], expected)
            raise OSError("signal delivery failed")
        with patch.object(peer, "validate"), patch.object(peer, "load_lease", return_value=self.data()), \
             patch.object(peer, "identity", return_value=self.identity), patch.object(peer, "fenced_signal", side_effect=stopped):
            with self.assertRaises(OSError):
                peer.execute(dict(self.request, action="pause"))
        self.assertEqual(json.loads(self.lease.read_text())["paused"], self.identity)

    def test_crash_disconnect_retains_restart_intent_without_repeating_kill(self):
        def killed(expected, sig):
            self.assertEqual(sig, signal.SIGKILL)
            self.assertTrue(json.loads(self.lease.read_text())["crash_requested"])
            raise ConnectionError("controller disconnected after signal")
        with patch.object(peer, "validate"), patch.object(peer, "load_lease", return_value=self.data()), \
             patch.object(peer, "fenced_signal", side_effect=killed):
            with self.assertRaises(ConnectionError):
                peer.execute(dict(self.request, action="crash"))
        retained = json.loads(self.lease.read_text())
        with patch.object(peer, "restart_reviewed", return_value=self.identity) as restart, \
             patch.object(peer, "fenced_signal") as send:
            self.assertTrue(peer.resume(self.request, retained)["restored"])
            restart.assert_called_once_with(self.request, retained)
            send.assert_not_called()
        with patch.object(peer, "validate"), patch.object(peer, "load_lease", return_value=retained), \
             patch.object(peer, "fenced_signal") as send:
            with self.assertRaisesRegex(ValueError, "already claimed"):
                peer.execute(dict(self.request, action="crash"))
            send.assert_not_called()

    def test_leak_checker_requires_complete_success_and_both_reviewed_files(self):
        request = dict(self.request, leak_files={"leakcheck.sh": "script", "leakcheck_native_loops.py": "helper"})
        with patch.object(peer, "command", return_value="LEAK: loop\nleakcheck OK — no leaked resources"):
            with self.assertRaises(ValueError):
                peer.leakcheck(request)
        with patch.object(peer, "command", return_value="leakcheck OK — no leaked resources"):
            self.assertTrue(peer.leakcheck(request)["passed"])
        request["leak_files"].pop("leakcheck_native_loops.py")
        with self.assertRaises(ValueError):
            peer.leakcheck(request)


if __name__ == "__main__":
    unittest.main()
