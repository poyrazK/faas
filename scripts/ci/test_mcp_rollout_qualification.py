"""Local contract checks; these deliberately do not qualify native hosting."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('rollout', ROOT / 'scripts/ops/mcp-hosting-rollout-qualification.py')
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class QualificationTests(unittest.TestCase):
    def test_prepare_isolated_failure_and_handler_fixtures(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'fixtures'
            MODULE.prepare(ROOT / 'cmd/gregale/templates/mcp-node', target)
            for name in ('web', 'web-bad', 'worker-old', 'worker-candidate', 'observer'):
                source = target / name
                config = json.loads((source / 'gregale-mcp.json').read_text())
                self.assertTrue(config['tasks']['enabled'])
                self.assertEqual(config['tasks']['namespace_env'], 'MCP_TASK_NAMESPACE')
                self.assertEqual(config['tasks']['database_url_env'], 'DATABASE_URL')
                self.assertFalse((source / 'node_modules').exists())
                generation = 'old' if name == 'worker-old' else 'candidate'
                self.assertIn("const generation = '" + generation + "'", (source / 'tasks.js').read_text())
            self.assertIn('res.status(503)', (target / 'web-bad/app.js').read_text())
            self.assertNotIn('res.status(503)', (target / 'web/app.js').read_text())
            self.assertIn('mcp_qualification_observer_role', (target / 'observer/tasks-observer.js').read_text())
            with self.assertRaises(ValueError):
                MODULE.prepare(ROOT / 'cmd/gregale/templates/mcp-node', target)

    def test_missing_credentials_are_blocked_and_redacted(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory) / 'blocked.json'
            args = argparse.Namespace(commit='a' * 40, scenario='rollout', evidence=evidence)
            with patch.dict(os.environ, {}, clear=True):
                report = MODULE.qualify(args)
            self.assertEqual(report['status'], 'blocked')
            self.assertFalse(report['native_observations'])
            self.assertEqual(json.loads(evidence.read_text()), report)
            self.assertEqual(evidence.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                MODULE.qualify(args)

    def test_binary_and_source_provenance_must_match_reviewed_commit(self):
        commit = 'a' * 40
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / 'gregale'
            binary.write_bytes(b'fixture')

            def execute(command, _env):
                if command[0] == 'git' and command[4] == 'HEAD':
                    return SimpleNamespace(stdout=commit + '\n')
                if command[0] == 'git':
                    return SimpleNamespace(stdout='')
                if command[:3] == ['go', 'version', '-m']:
                    return SimpleNamespace(stdout=(
                        'build\tvcs=git\n'
                        f'build\tvcs.revision={commit}\n'
                        'build\tvcs.time=2026-10-09T00:00:00Z\n'
                        'build\tvcs.modified=false\n'
                    ))
                self.fail(f'unexpected provenance command: {command}')

            with patch.object(MODULE, 'execute', side_effect=execute):
                provenance = MODULE.verify_release_provenance(binary, commit, root)
            self.assertEqual(provenance, {
                'source_revision': commit,
                'binary_revision': commit,
                'binary_modified': False,
            })

            with patch.object(MODULE, 'execute', side_effect=execute):
                with self.assertRaisesRegex(MODULE.Blocked, 'source checkout does not match'):
                    MODULE.verify_release_provenance(binary, 'b' * 40, root)

            def dirty_checkout(command, _env):
                if command[0] == 'git' and command[4] == 'HEAD':
                    return SimpleNamespace(stdout=commit + '\n')
                if command[0] == 'git':
                    return SimpleNamespace(stdout=' M cmd/gregale/main.go\n')
                self.fail('binary inspection ran for a dirty checkout')

            with patch.object(MODULE, 'execute', side_effect=dirty_checkout):
                with self.assertRaisesRegex(MODULE.Blocked, 'clean source checkout'):
                    MODULE.verify_release_provenance(binary, commit, root)

            def wrong_binary_revision(command, env):
                result = execute(command, env)
                if command[:3] == ['go', 'version', '-m']:
                    return SimpleNamespace(stdout=result.stdout.replace(commit, 'b' * 40))
                return result

            with patch.object(MODULE, 'execute', side_effect=wrong_binary_revision):
                with self.assertRaisesRegex(MODULE.Blocked, 'binary VCS revision'):
                    MODULE.verify_release_provenance(binary, commit, root)

            def modified_binary(command, env):
                result = execute(command, env)
                if command[:3] == ['go', 'version', '-m']:
                    return SimpleNamespace(stdout=result.stdout.replace('vcs.modified=false', 'vcs.modified=true'))
                return result

            with patch.object(MODULE, 'execute', side_effect=modified_binary):
                with self.assertRaisesRegex(MODULE.Blocked, 'modified source'):
                    MODULE.verify_release_provenance(binary, commit, root)

    def test_command_errors_do_not_expose_secrets(self):
        with self.assertRaisesRegex(RuntimeError, '^Qualification command failed$'):
            MODULE.execute(['python3', '-c', "import sys; print('postgres://secret'); sys.exit(1)"], dict(os.environ))


if __name__ == '__main__':
    unittest.main()
