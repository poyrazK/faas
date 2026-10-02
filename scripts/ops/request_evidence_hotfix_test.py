"""Contract tests: refusal before activation, exact artifact and multi-host rollback."""
import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

import request_evidence_hotfix as controller
import request_evidence_hotfix_host as host
import request_evidence_hotfix_github as github
import zipfile
from email.message import Message
import urllib.error


class HotfixContracts(unittest.TestCase):
    def test_rollback_cannot_restart_a_later_full_release(self):
        with patch.object(Path, 'resolve', return_value=Path('/opt/faas/releases/later-source')), \
             patch.object(host, 'command') as unit, patch.object(Path, 'unlink') as remove:
            with self.assertRaises(AssertionError):
                host.rollback()
            unit.assert_not_called()
            remove.assert_not_called()

    def test_signed_storage_download_does_not_receive_the_github_token(self):
        headers = Message()
        headers['Location'] = 'https://storage.example.test/artifact?signature=public-test'
        response = SimpleNamespace(read=lambda _: b'artifact')
        with patch.dict('os.environ', {'GH_TOKEN': 'secret-test-token'}), \
             patch.object(github.urllib.request, 'build_opener') as opener, \
             patch.object(github.urllib.request, 'urlopen') as storage:
            opener.return_value.open.side_effect = urllib.error.HTTPError('https://api.github.com', 302, 'redirect', headers, None)
            storage.return_value.__enter__.return_value = response
            self.assertEqual(github.artifact_bytes(123), b'artifact')
            self.assertEqual(storage.call_args.args, (headers['Location'],))
            self.assertEqual(storage.call_args.kwargs, {'timeout': 60})

    def test_artifact_cannot_escape_its_owned_directory(self):
        data = io.BytesIO()
        with zipfile.ZipFile(data, 'w') as archive:
            archive.writestr('../outside.json', '{}')
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(ValueError):
                github.extract_metadata(data.getvalue(), Path(tmp) / 'owned')
            self.assertFalse((Path(tmp) / 'outside.json').exists())

    def test_inspection_never_opens_a_writable_file_or_changes_a_unit(self):
        state = {'gateway': {'sha256': host.OLD_HASH, 'pid': 123}}
        with patch.object(host, 'snapshot', return_value=state), patch.object(host, 'health'), \
             patch.object(Path, 'read_bytes', return_value=b'DATABASE_URL=postgresql://example\x00'), \
             patch.object(host.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout='{}')) as query, \
             patch('builtins.open', side_effect=AssertionError('write during inspection')), \
             patch.object(host, 'command') as unit:
            result = host.main({'source': host.SOURCE, 'base': host.BASE, 'action': 'inspect'})
            self.assertEqual(result['status'], 'inspected')
            self.assertIn('BEGIN READ ONLY;', query.call_args.kwargs['input'])
            unit.assert_not_called()

    def test_green_postgres_summary_cannot_hide_a_skipped_subtest(self):
        log = ('=== RUN   TestExclusivePolicyRetirement\n'
               '=== RUN   TestExclusivePolicyRetirement/postgres\n'
               '--- PASS: TestExclusivePolicyRetirement/postgres (1s)\n'
               '--- PASS: TestExclusivePolicyRetirement (1s)\n'
               'operation-policy-postgres-check: PostgreSQL retirement passed\n')
        controller.validate_postgres(log)
        with self.assertRaises(AssertionError):
            controller.validate_postgres(log.replace('--- PASS: TestExclusivePolicyRetirement/postgres',
                                                     '--- SKIP: TestExclusivePolicyRetirement/postgres'))

    def test_second_host_failure_rolls_back_both_attempted_hosts(self):
        events = []
        def remote(target, action):
            events.append((target, action))
            if target == controller.TARGETS[1][0] and action == 'apply':
                raise RuntimeError('gate failed')
            return {'status': action}
        with self.assertRaises(RuntimeError):
            controller.deploy(remote, lambda x: None, lambda: None, lambda *x: None)
        self.assertEqual(events[-2:], [(controller.TARGETS[1][0], 'rollback'), (controller.TARGETS[0][0], 'rollback')])

    def test_ingress_failure_stops_before_second_host_and_rolls_back_first(self):
        events = []
        def gate():
            raise RuntimeError('ingress failed')
        with self.assertRaises(RuntimeError):
            controller.deploy(lambda t, a: events.append((t, a)), lambda x: None, gate, lambda *x: None)
        self.assertEqual(events[-1], (controller.TARGETS[0][0], 'rollback'))
        self.assertFalse(any(t == controller.TARGETS[1][0] for t, _ in events))

    def test_staged_hash_mismatch_cannot_restart_or_change_configuration(self):
        with patch.object(host, 'digest', return_value='wrong'), patch.object(host, 'command') as cmd, patch.object(host, 'write_atomic') as write:
            with self.assertRaises(AssertionError):
                host.apply({'gateway_sha256': 'expected'})
            cmd.assert_not_called()
            write.assert_not_called()

    def test_unrelated_override_is_never_removed(self):
        with tempfile.TemporaryDirectory() as tmp:
            drop = Path(tmp) / 'override.conf'; drop.write_text('unrelated')
            with patch.object(host, 'DROP', drop), patch.object(host, 'command') as cmd, \
                 patch.object(Path, 'resolve', return_value=Path('/opt/faas/releases/' + host.BASE)):
                with self.assertRaises(AssertionError):
                    host.rollback()
                self.assertEqual(drop.read_text(), 'unrelated')
                cmd.assert_not_called()

    def test_archive_must_contain_exact_signed_manifest_and_binary(self):
        data = b'test gateway'
        manifest = json.dumps({'git_sha': host.SOURCE, 'daemon_hashes': {'gatewayd_internal': 'sha256:' + hashlib.sha256(data).hexdigest()}}).encode()
        with tempfile.TemporaryDirectory() as tmp:
            archive = Path(tmp) / 'release.tar.gz'; out = Path(tmp) / 'gateway'
            with tarfile.open(archive, 'w:gz') as tb:
                for name, content in [('release-manifest.json', manifest), ('gatewayd-internal', data)]:
                    info = tarfile.TarInfo(name); info.size = len(content); tb.addfile(info, io.BytesIO(content))
            self.assertEqual(controller.extract_gateway(archive, manifest, out), hashlib.sha256(data).hexdigest())
            changed = json.loads(manifest); changed['daemon_hashes']['gatewayd_internal'] = 'sha256:' + '0' * 64
            with self.assertRaises(AssertionError):
                controller.extract_gateway(archive, json.dumps(changed).encode(), out)


if __name__ == '__main__':
    unittest.main()
