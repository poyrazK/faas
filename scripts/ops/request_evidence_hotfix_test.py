"""Contract tests: refusal before activation, exact artifact and multi-host rollback."""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from email.message import Message
from unittest.mock import patch
from types import SimpleNamespace
import urllib.request

import request_evidence_hotfix as controller
import request_evidence_hotfix_host as host


class HotfixContracts(unittest.TestCase):
    def test_real_ssh_failure_retains_safe_diagnostics_without_process_data(self):
        with tempfile.TemporaryDirectory() as tmp:
            ssh = Path(tmp) / 'ssh'
            ssh.write_text('#!' + sys.executable + '\n'
                           'import sys\n'
                           'print("private stdout must not enter the receipt")\n'
                           'print("ssh: connect to host fsn-2.gregale.dev port 22: Connection refused", file=sys.stderr)\n'
                           'print("private stderr must not enter the receipt", file=sys.stderr)\n'
                           'sys.exit(255)\n')
            ssh.chmod(0o700)
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                controller.run([str(ssh), '-i', 'private-key-path', 'root@fsn-2.gregale.dev',
                                'private remote command'], input='private stdin')
        details = controller.failure_details(raised.exception)
        self.assertEqual(details, {'error_type': 'CalledProcessError', 'command': 'ssh',
                                  'returncode': 255, 'target': 'fsn-2.gregale.dev',
                                  'diagnostic': 'connection_refused'})
        self.assertNotIn('private', json.dumps(details))

    def test_ssh_diagnostics_classify_common_failures_without_echoing_banners(self):
        cases = [
            ('Permission denied (publickey).', 'authentication_failed'),
            ('Connection timed out', 'connection_timed_out'),
            ('Could not resolve hostname secret-name', 'name_resolution_failed'),
            ('Host key verification failed.', 'host_key_verification_failed'),
            ('WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!', 'host_key_verification_failed'),
            ('No route to host', 'network_unreachable'),
            ('Connection reset by peer', 'connection_closed'),
            ('Load key "secret-path": invalid format', 'key_load_failed'),
            ('exec request failed on channel 0', 'remote_command_rejected'),
            ('arbitrary secret banner\x1b[31m', 'ssh_failed'),
        ]
        for stderr, diagnostic in cases:
            with self.subTest(diagnostic=diagnostic, stderr=stderr):
                error = subprocess.CalledProcessError(255, ['ssh', 'root@fsn-2.gregale.dev'],
                                                      stderr=stderr.encode())
                details = controller.failure_details(error)
                self.assertEqual(details['diagnostic'], diagnostic)
                self.assertNotIn('secret', json.dumps(details))
                self.assertNotIn('\x1b', json.dumps(details))

    def test_real_process_timeout_retains_limit_and_never_echoes_output(self):
        with self.assertRaises(subprocess.TimeoutExpired) as raised:
            controller.run([sys.executable, '-c',
                            'import time; print("secret", flush=True); time.sleep(5)'], timeout=0.1)
        details = controller.failure_details(raised.exception)
        self.assertEqual(details['diagnostic'], 'process_timeout')
        self.assertEqual(details['timeout_seconds'], 0.1)
        self.assertNotIn('secret', json.dumps(details))
        self.assertNotIn('time.sleep', json.dumps(details))

    def test_remote_exit_is_not_misclassified_as_ssh_authentication_failure(self):
        error = subprocess.CalledProcessError(1, ['ssh', 'root@fsn-2.gregale.dev'],
                                              stderr='Traceback: Permission denied reading a remote file')
        details = controller.failure_details(error)
        self.assertEqual(details['diagnostic'], 'remote_command_failed')
        self.assertEqual(details['returncode'], 1)

    def test_failed_inspection_records_and_prints_only_safe_process_details(self):
        error = subprocess.CalledProcessError(255, ['ssh', 'root@fsn-2.gregale.dev'],
                                              output='secret output', stderr='Permission denied (publickey). secret')
        with tempfile.TemporaryDirectory() as tmp:
            receipt = Path(tmp) / 'receipt.json'
            stderr = io.StringIO()
            with patch.object(controller, 'RECEIPT', receipt), \
                 patch.object(controller, 'qualify', side_effect=error), \
                 patch.dict(os.environ, {'HOTFIX_TAG': 'v0.1.18-rc.232', 'COMPUTE_SSH_KEY': 'unused'}), \
                 patch.object(sys, 'argv', ['inspector', '--cosign', 'unused']), \
                 patch.object(sys, 'stderr', stderr), \
                 patch.object(controller.signal, 'signal'), \
                 patch.object(controller, 'deploy') as deploy:
                with self.assertRaises(subprocess.CalledProcessError) as raised:
                    controller.main()
            self.assertIs(raised.exception, error)
            deploy.assert_not_called()
            value = json.loads(receipt.read_text())
        self.assertEqual(value['status'], 'failed')
        failure = value['events'][-1]['result']
        self.assertEqual(failure['returncode'], 255)
        self.assertEqual(failure['target'], 'fsn-2.gregale.dev')
        self.assertEqual(failure['diagnostic'], 'authentication_failed')
        self.assertEqual(json.loads(stderr.getvalue()), {'failure': failure})
        self.assertNotIn('secret', json.dumps(value) + stderr.getvalue())

    def test_rollback_failure_retains_diagnostic_without_masking_original_failure(self):
        original = RuntimeError('ingress failed')
        events = []
        rollback_error = subprocess.CalledProcessError(255, ['ssh', 'root@fsn-2.gregale.dev'],
                                                       stderr='Connection timed out; secret')
        def remote(target, action):
            if action == 'rollback':
                raise rollback_error
            return {'status': action}
        def gate():
            raise original
        with self.assertRaises(RuntimeError) as raised:
            controller.deploy(remote, lambda _: None, gate, lambda target, value: events.append((target, value)))
        self.assertIs(raised.exception, original)
        failures = next(value for target, value in events if target == 'rollback_failures')
        self.assertEqual(failures[0]['target'], 'fsn-2.gregale.dev')
        self.assertEqual(failures[0]['returncode'], 255)
        self.assertEqual(failures[0]['diagnostic'], 'connection_timed_out')
        self.assertNotIn('secret', json.dumps(failures))

    def test_github_api_helper_does_not_depend_on_gh_cli(self):
        with patch.object(controller, 'github_get', return_value=b'{"workflow_runs":[]}') as get:
            self.assertEqual(controller.api('actions/runs?per_page=100'), {'workflow_runs': []})
        get.assert_called_once_with('repos/' + controller.REPO + '/actions/runs?per_page=100')

    def test_image_verifier_uses_the_shared_github_api_helper(self):
        verifier = Path(controller.__file__).with_name('request_evidence_hotfix_images.py').read_text()
        self.assertIn("controller.api('actions/runs/'", verifier)
        self.assertNotIn("['gh'", verifier)

    def test_github_token_is_removed_before_redirect_to_asset_storage(self):
        request = urllib.request.Request('https://api.github.com/repos/example/release', headers={
            'Authorization': 'Bearer secret', 'X-GitHub-Api-Version': '2022-11-28',
        })
        redirected = controller.GithubRedirectHandler().redirect_request(
            request, None, 302, 'Found', Message(), 'https://objects.githubusercontent.com/release'
        )
        self.assertIsNone(redirected.get_header('Authorization'))
        self.assertIsNone(redirected.get_header('X-github-api-version'))

    def test_release_asset_download_streams_to_disk(self):
        with tempfile.TemporaryDirectory() as tmp:
            destination = Path(tmp) / 'release.tar.gz'
            with patch.object(controller, 'github_open', return_value=io.BytesIO(b'large asset')) as open_asset:
                controller.github_download('repos/example/assets/1', destination)
            self.assertEqual(destination.read_bytes(), b'large asset')
        open_asset.assert_called_once_with('repos/example/assets/1', 'application/octet-stream')

    def test_inspection_never_opens_a_writable_file_or_changes_a_unit(self):
        state = {'gateway': {'sha256': host.OLD_HASH, 'pid': 123,
                             'exe': '/opt/faas/releases/' + host.BASE + '/bin/gatewayd-internal'}}
        with patch.object(host, 'snapshot', return_value=state), patch.object(host, 'health'), \
             patch.object(Path, 'read_bytes', return_value=b'DATABASE_URL=postgresql://example\x00'), \
             patch.object(host.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout='{}')) as query, \
             patch('builtins.open', side_effect=AssertionError('write during inspection')), \
             patch.object(host, 'command') as unit:
            result = host.main({'source': host.SOURCE, 'base': host.BASE, 'action': 'inspect'})
            self.assertEqual(result['status'], 'inspected')
            self.assertIn('BEGIN READ ONLY;', query.call_args.kwargs['input'])
        unit.assert_not_called()

    def test_inspection_accepts_exact_signed_active_hotfix_without_writes(self):
        gateway_hash = '10f40bdc3fee686eec5e6c0d9b2ce9c57fd5c8d3a5abc584b6c58da109289445'
        state = {'gateway': {'sha256': gateway_hash, 'pid': 123, 'exe': str(host.BINARY)}}
        with tempfile.TemporaryDirectory() as tmp:
            drop = Path(tmp) / 'override.conf'
            drop.write_text(host.CONTENT)
            with patch.object(host, 'DROP', drop), patch.object(host, 'snapshot', return_value=state), \
                 patch.object(host, 'health'), \
                 patch.object(Path, 'read_bytes', return_value=b'DATABASE_URL=postgresql://example\x00'), \
                 patch.object(host.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout='{}')) as query, \
                 patch('builtins.open', side_effect=AssertionError('write during inspection')), \
                 patch.object(host, 'write_atomic', side_effect=AssertionError('write during inspection')), \
                 patch.object(controller.signal, 'signal'), \
                 patch.object(host, 'command') as unit:
                result = host.main({'source': host.SOURCE, 'base': host.BASE, 'action': 'inspect',
                                    'gateway_sha256': gateway_hash})
            self.assertEqual(result['status'], 'inspected')
            self.assertEqual(result['snapshot'], state)
            self.assertIn('BEGIN READ ONLY;', query.call_args.kwargs['input'])
            unit.assert_not_called()

    def test_inspection_refuses_unqualified_binary_or_override_before_query(self):
        gateway_hash = '10f40bdc3fee686eec5e6c0d9b2ce9c57fd5c8d3a5abc584b6c58da109289445'
        base_exe = '/opt/faas/releases/' + host.BASE + '/bin/gatewayd-internal'
        cases = [
            (None, 'wrong', base_exe),
            (None, host.OLD_HASH, str(host.BINARY)),
            (host.CONTENT, 'wrong', str(host.BINARY)),
            (host.CONTENT, gateway_hash, base_exe),
            ('unrelated override', gateway_hash, str(host.BINARY)),
        ]
        for content, sha, exe in cases:
            with self.subTest(content=content, sha=sha, exe=exe), tempfile.TemporaryDirectory() as tmp:
                drop = Path(tmp) / 'override.conf'
                if content is not None:
                    drop.write_text(content)
                state = {'gateway': {'sha256': sha, 'pid': 123, 'exe': exe}}
                with patch.object(host, 'DROP', drop), patch.object(host, 'snapshot', return_value=state), \
                     patch.object(host, 'health'), patch.object(Path, 'read_bytes') as read_env, \
                     patch.object(host.subprocess, 'run') as query, \
                     patch.object(host, 'write_atomic', side_effect=AssertionError('unexpected write')), \
                     patch.object(controller.signal, 'signal'), patch.object(host, 'command') as unit:
                    with self.assertRaises(AssertionError):
                        host.main({'source': host.SOURCE, 'base': host.BASE, 'action': 'inspect',
                                   'gateway_sha256': gateway_hash})
                    read_env.assert_not_called()
                    query.assert_not_called()
                    unit.assert_not_called()
                if content is not None:
                    self.assertEqual(drop.read_text(), content)

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
            with patch.object(host, 'DROP', drop), patch.object(host, 'command') as cmd:
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
